package api

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/rcobb/openlabstats-server/internal/store"
)

const (
	// agentMetricPrefix is the only metric family namespace accepted from
	// agents; everything else (go_*, process_*, forged names) is dropped.
	agentMetricPrefix = "openlabstats_"
	// maxSeriesPerFamily bounds one agent's cardinality per family.
	maxSeriesPerFamily = 50000
	// maxLabelValueBytes bounds a single label value.
	maxLabelValueBytes = 1024
)

// serverOwnedLabels are set by the server from registration data, never
// trusted from the agent.
var serverOwnedLabels = []string{"hostname", "lab", "building", "room"}

// parseAgentMetrics parses an agent's text exposition, enforces size limits and
// keeps only openlabstats_* families. Any parse or limit violation is an error
// (the caller keeps the previous snapshot).
func parseAgentMetrics(body []byte) (map[string]*dto.MetricFamily, error) {
	parser := expfmt.NewTextParser(model.LegacyValidation)
	all, err := parser.TextToMetricFamilies(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("malformed metrics: %w", err)
	}
	out := make(map[string]*dto.MetricFamily, len(all))
	for name, mf := range all {
		if !strings.HasPrefix(name, agentMetricPrefix) {
			continue
		}
		if len(mf.Metric) > maxSeriesPerFamily {
			return nil, fmt.Errorf("family %q has %d series (max %d)", name, len(mf.Metric), maxSeriesPerFamily)
		}
		for _, m := range mf.Metric {
			for _, lp := range m.Label {
				if len(lp.GetValue()) > maxLabelValueBytes {
					return nil, fmt.Errorf("family %q: label %q value exceeds %d bytes", name, lp.GetName(), maxLabelValueBytes)
				}
			}
		}
		out[name] = mf
	}
	return out, nil
}

func getLabel(m *dto.Metric, name string) string {
	for _, lp := range m.Label {
		if lp.GetName() == name {
			return lp.GetValue()
		}
	}
	return ""
}

func setLabel(m *dto.Metric, name, value string) {
	for _, lp := range m.Label {
		if lp.GetName() == name {
			lp.Value = &value
			return
		}
	}
	m.Label = append(m.Label, &dto.LabelPair{Name: &name, Value: &value})
}

func deleteLabels(m *dto.Metric, names ...string) {
	kept := m.Label[:0]
	for _, lp := range m.Label {
		drop := false
		for _, n := range names {
			if lp.GetName() == n {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, lp)
		}
	}
	m.Label = kept
}

// applyMappingsToFamilies applies the server-side software mappings to parsed
// families: ignored exes and built-in ignored exes are dropped, manually
// reviewed mappings rewrite app/category, auto mappings pass through, and exes
// with no mapping are returned (keyed by lowercased exe) with the agent's own
// app/category as a hint for auto-insert. Families left empty are removed.
func applyMappingsToFamilies(fams map[string]*dto.MetricFamily, mappings map[string]*store.SoftwareMapping) map[string]store.DiscoveredApp {
	unknown := make(map[string]store.DiscoveredApp)
	for name, mf := range fams {
		kept := mf.Metric[:0]
		for _, m := range mf.Metric {
			exe := getLabel(m, "exe")
			if exe == "" {
				kept = append(kept, m)
				continue
			}
			key := strings.ToLower(exe)
			mp, found := mappings[key]
			if !found {
				if isBuiltInIgnoredExe(key) {
					continue
				}
				if _, seen := unknown[key]; !seen {
					unknown[key] = store.DiscoveredApp{
						DisplayName: getLabel(m, "app"),
						Category:    getLabel(m, "category"),
					}
				}
				kept = append(kept, m)
				continue
			}
			if mp.Ignored {
				continue
			}
			if mp.Source != "auto" {
				// Only rewrite labels the sample already carries.
				if hasLabel(m, "app") {
					setLabel(m, "app", mp.DisplayName)
				}
				if mp.Category != "" && hasLabel(m, "category") {
					setLabel(m, "category", mp.Category)
				}
			}
			kept = append(kept, m)
		}
		mf.Metric = kept
		if len(kept) == 0 {
			delete(fams, name)
		}
	}
	return unknown
}

func hasLabel(m *dto.Metric, name string) bool {
	for _, lp := range m.Label {
		if lp.GetName() == name {
			return true
		}
	}
	return false
}

// stampServerLabels strips agent-supplied lab/building/room labels and sets
// the registered hostname plus any server-known lab info. A series whose own
// hostname label names a different host is dropped rather than relabelled: a
// Mac renamed while the agent runs still exposes series under its old name,
// and forcing those onto the new name would collide with the current ones (and
// a spoofing agent would get its foreign series silently re-homed). Any series
// that still collapses onto an earlier label set is dropped too, so one odd
// series never costs the agent its whole push. It returns how many were dropped.
func stampServerLabels(fams map[string]*dto.MetricFamily, hostname string, extra map[string]string) int {
	dropped := 0
	for name, mf := range fams {
		seen := make(map[string]struct{}, len(mf.Metric))
		kept := mf.Metric[:0]
		for _, m := range mf.Metric {
			if h := getLabel(m, "hostname"); h != "" && !strings.EqualFold(h, hostname) {
				dropped++
				continue
			}
			deleteLabels(m, serverOwnedLabels...)
			setLabel(m, "hostname", hostname)
			for k, v := range extra {
				setLabel(m, k, v)
			}
			sort.Slice(m.Label, func(i, j int) bool { return m.Label[i].GetName() < m.Label[j].GetName() })
			sig := metricSignature(m)
			if _, dup := seen[sig]; dup {
				dropped++
				continue
			}
			seen[sig] = struct{}{}
			kept = append(kept, m)
		}
		mf.Metric = kept
		if len(kept) == 0 {
			delete(fams, name)
		}
	}
	return dropped
}

func metricSignature(m *dto.Metric) string {
	var sb strings.Builder
	for _, lp := range m.Label {
		sb.WriteString(lp.GetName())
		sb.WriteByte(0xff)
		sb.WriteString(lp.GetValue())
		sb.WriteByte(0xfe)
	}
	return sb.String()
}

// mergeAgentFamilies merges the per-agent families into one family per name.
// A family whose type conflicts with an earlier one is dropped (logged), and a
// series that duplicates an already-merged one is skipped, so the encoded page
// is always valid exposition.
func mergeAgentFamilies(snapshots []map[string]*dto.MetricFamily, logger *slog.Logger) []*dto.MetricFamily {
	merged := make(map[string]*dto.MetricFamily)
	seen := make(map[string]map[string]struct{})
	for _, snap := range snapshots {
		names := make([]string, 0, len(snap))
		for n := range snap {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			mf := snap[name]
			dst, ok := merged[name]
			if !ok {
				dst = &dto.MetricFamily{Name: mf.Name, Help: mf.Help, Type: mf.Type}
				merged[name] = dst
				seen[name] = make(map[string]struct{})
			} else if dst.GetType() != mf.GetType() {
				if logger != nil {
					logger.Warn("dropping agent metric family with conflicting type",
						"family", name, "type", mf.GetType().String(), "expected", dst.GetType().String())
				}
				continue
			}
			for _, m := range mf.Metric {
				sig := metricSignature(m)
				if _, dup := seen[name][sig]; dup {
					continue
				}
				seen[name][sig] = struct{}{}
				dst.Metric = append(dst.Metric, m)
			}
		}
	}
	names := make([]string, 0, len(merged))
	for n := range merged {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*dto.MetricFamily, 0, len(names))
	for _, n := range names {
		out = append(out, merged[n])
	}
	return out
}

// encodeFamilies writes families as Prometheus text exposition.
func encodeFamilies(w io.Writer, fams []*dto.MetricFamily) error {
	enc := expfmt.NewEncoder(w, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, mf := range fams {
		if err := enc.Encode(mf); err != nil {
			return err
		}
	}
	return nil
}
