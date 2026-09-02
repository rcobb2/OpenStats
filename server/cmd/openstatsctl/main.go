// Command openstatsctl is a client for a live OpenStats instance, built for
// agents and people alike: every subcommand prints an aligned table by default
// and raw JSON with --json.
//
// It deliberately exposes reads plus low-risk writes only. Deleting an agent,
// forcing a fleet update, and changing fleet settings are reachable through the
// REST API and the web portal, and are left out here on purpose — an agent
// working from a mistaken premise should not be able to disrupt the fleet. See
// the "excluded" note in usage().
package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	// --server / OPENSTATS_URL may appear anywhere; pull it out before dispatch.
	args, server := extractServerFlag(os.Args[1:])
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	c := newClient(server)
	if err := dispatch(c, args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func dispatch(c *client, args []string) error {
	switch args[0] {
	case "help", "-h", "--help":
		usage()
		return nil
	case "version":
		return cmdVersion(c, args[1:])
	case "status":
		return cmdStatus(c, args[1:])
	case "agents":
		return cmdAgents(c, args[1:])
	case "labs":
		return cmdLabs(c, args[1:])
	case "users":
		return cmdUsers(c, args[1:])
	case "mappings":
		return cmdMappings(c, args[1:])
	case "reports":
		return cmdReports(c, args[1:])
	case "settings":
		return cmdSettings(c, args[1:])
	default:
		return fmt.Errorf("unknown command %q (try: openstatsctl help)", args[0])
	}
}

// --- version / status ---

func cmdVersion(c *client, args []string) error {
	var info buildInfo
	if err := c.get("/version", nil, &info); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emitJSON(info)
	}
	fmt.Printf("server:     %s\n", c.baseURL)
	fmt.Printf("version:    %s\n", info.Version)
	fmt.Printf("commit:     %s\n", info.GitCommit)
	fmt.Printf("built:      %s\n", info.BuildDate)
	fmt.Printf("go:         %s\n", info.GoVersion)
	return nil
}

func cmdStatus(c *client, args []string) error {
	var s summary
	if err := c.get("/reports/summary", nil, &s); err != nil {
		return err
	}
	var info buildInfo
	if err := c.get("/version", nil, &info); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emitJSON(map[string]interface{}{"summary": s, "build": info})
	}
	fmt.Printf("server:     %s (%s @ %s)\n", c.baseURL, info.Version, info.GitCommit)
	fmt.Printf("agents:     %d total, %d online\n", s.TotalAgents, s.OnlineAgents)
	fmt.Printf("labs:       %d\n", s.TotalLabs)
	fmt.Printf("mappings:   %d\n", s.TotalMappings)
	return nil
}

// --- agents ---

func cmdAgents(c *client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: openstatsctl agents list|get|assign")
	}
	switch args[0] {
	case "list":
		return agentsList(c, args[1:])
	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: openstatsctl agents get <agent-id>")
		}
		var a agent
		if err := c.get("/agents/"+url.PathEscape(args[1]), nil, &a); err != nil {
			return err
		}
		return emitJSON(a)
	case "assign":
		return agentsAssign(c, args[1:])
	default:
		return fmt.Errorf("unknown agents subcommand %q", args[0])
	}
}

func agentsList(c *client, args []string) error {
	var agents []agent
	if err := c.get("/agents", nil, &agents); err != nil {
		return err
	}
	labName := labNames(c)

	statusFilter := strings.ToLower(flagValue(args, "--status"))
	labFilter := strings.ToLower(flagValue(args, "--lab"))

	filtered := agents[:0]
	for _, a := range agents {
		if statusFilter != "" && strings.ToLower(a.Status) != statusFilter {
			continue
		}
		if labFilter != "" && !strings.Contains(strings.ToLower(labName[derefLab(a.LabID)]), labFilter) {
			continue
		}
		filtered = append(filtered, a)
	}

	if hasFlag(args, "--json") {
		return emitJSON(filtered)
	}

	t := newTable("HOSTNAME", "STATUS", "VERSION", "LAB", "IP", "LAST SEEN")
	for _, a := range filtered {
		t.add(a.Hostname, a.Status, a.AgentVersion,
			orDash(labName[derefLab(a.LabID)]), orDash(a.IPAddress),
			a.LastSeen.Local().Format("2006-01-02 15:04"))
	}
	t.render(os.Stdout)
	return nil
}

func agentsAssign(c *client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: openstatsctl agents assign <agent-id> --lab <lab-id>")
	}
	labID := flagValue(args, "--lab")
	if labID == "" {
		return fmt.Errorf("--lab <lab-id> is required (see: openstatsctl labs list)")
	}
	agentID := args[0]
	if err := c.send("PUT", "/agents/"+url.PathEscape(agentID)+"/lab",
		map[string]string{"labId": labID}); err != nil {
		return err
	}
	fmt.Printf("assigned %s to lab %s on %s\n", agentID, labID, c.baseURL)
	return nil
}

// --- labs ---

func cmdLabs(c *client, args []string) error {
	if len(args) == 0 || args[0] != "list" {
		return fmt.Errorf("usage: openstatsctl labs list")
	}
	var labs []lab
	if err := c.get("/labs", nil, &labs); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emitJSON(labs)
	}
	t := newTable("ID", "NAME", "BUILDING", "ROOM")
	for _, l := range labs {
		t.add(l.ID, l.Name, orDash(l.Building), orDash(l.Room))
	}
	t.render(os.Stdout)
	return nil
}

func labNames(c *client) map[string]string {
	var labs []lab
	out := map[string]string{}
	if err := c.get("/labs", nil, &labs); err != nil {
		return out // labels degrade to blank rather than failing the listing
	}
	for _, l := range labs {
		out[l.ID] = l.Name
	}
	return out
}

// --- users ---

func cmdUsers(c *client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: openstatsctl users list|rules|policy|ignore|unignore|alias")
	}
	switch args[0] {
	case "list":
		return usersList(c, args[1:])
	case "rules":
		// "rules rm <id>" deletes a rule; bare "rules" lists them. The tool can
		// create rules, so it must be able to clean up after itself.
		if len(args) >= 2 && args[1] == "rm" {
			return usersRulesRemove(c, args[2:])
		}
		return usersRules(c, args[1:])
	case "policy":
		var p userPolicy
		if err := c.get("/users/policy", nil, &p); err != nil {
			return err
		}
		return emitJSON(p)
	case "ignore":
		if len(args) < 2 {
			return fmt.Errorf("usage: openstatsctl users ignore <username-or-pattern>")
		}
		if err := c.send("POST", "/users/ignore", map[string]string{
			"user":  args[1],
			"notes": "Ignored via openstatsctl",
		}); err != nil {
			return err
		}
		fmt.Printf("ignored %q on %s (agents pick this up on their next heartbeat)\n", args[1], c.baseURL)
		return nil
	case "unignore":
		return usersUnignore(c, args[1:])
	case "alias":
		if len(args) < 3 {
			return fmt.Errorf("usage: openstatsctl users alias <pattern> <canonical-user>")
		}
		if err := c.send("PUT", "/users/mappings", map[string]interface{}{
			"pattern":       args[1],
			"canonicalUser": args[2],
			"notes":         "Alias added via openstatsctl",
			"ignored":       false,
		}); err != nil {
			return err
		}
		fmt.Printf("aliased %q -> %q on %s\n", args[1], args[2], c.baseURL)
		return nil
	default:
		return fmt.Errorf("unknown users subcommand %q", args[0])
	}
}

func usersList(c *client, args []string) error {
	params := url.Values{}
	if r := flagValue(args, "--range"); r != "" {
		params.Set("range", r)
	}
	var users []discoveredUser
	if err := c.get("/users", params, &users); err != nil {
		return err
	}

	onlyIgnored := hasFlag(args, "--ignored")
	onlyTracked := hasFlag(args, "--tracked")

	filtered := users[:0]
	for _, u := range users {
		if onlyIgnored && !u.Ignored {
			continue
		}
		if onlyTracked && u.Ignored {
			continue
		}
		filtered = append(filtered, u)
	}

	if hasFlag(args, "--json") {
		return emitJSON(filtered)
	}

	t := newTable("USER", "HOURS", "ACTIVE", "IGNORED", "SEEN AS")
	for _, u := range filtered {
		t.add(u.CanonicalUser, formatFloat(u.SessionHours), yesNo(u.ActiveNow),
			yesNo(u.Ignored), strings.Join(u.RawUsers, ", "))
	}
	t.render(os.Stdout)
	return nil
}

func usersRules(c *client, args []string) error {
	rules, err := fetchUserRules(c)
	if err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emitJSON(rules)
	}
	t := newTable("ID", "PATTERN", "MERGES INTO", "IGNORED", "SOURCE", "NOTES")
	for _, r := range rules {
		t.add(strconv.Itoa(r.ID), r.Pattern, orDash(r.CanonicalUser),
			yesNo(r.Ignored), r.Source, orDash(r.Notes))
	}
	t.render(os.Stdout)
	return nil
}

// usersUnignore resolves a username to the rule that ignores it. Built-in
// defaults have no rule to clear, so that case gets an explicit message rather
// than a silent no-op.
func usersUnignore(c *client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: openstatsctl users unignore <username-or-pattern>")
	}
	target := strings.ToLower(strings.TrimSpace(args[0]))

	rules, err := fetchUserRules(c)
	if err != nil {
		return err
	}
	cleared := 0
	for _, r := range rules {
		if !r.Ignored || strings.ToLower(r.Pattern) != target {
			continue
		}
		if err := c.send("PATCH", fmt.Sprintf("/users/mappings/%d/ignore", r.ID),
			map[string]bool{"ignored": false}); err != nil {
			return err
		}
		cleared++
	}
	if cleared == 0 {
		return fmt.Errorf("no editable ignore rule matches %q — it may be excluded by a "+
			"built-in default (system and service accounts), which cannot be cleared this way; "+
			"add an explicit alias instead: openstatsctl users alias %s %s", args[0], args[0], args[0])
	}
	fmt.Printf("cleared %d ignore rule(s) for %q on %s\n", cleared, args[0], c.baseURL)
	return nil
}

func usersRulesRemove(c *client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: openstatsctl users rules rm <rule-id>  (see: openstatsctl users rules)")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("rule id must be a number, got %q", args[0])
	}
	if err := c.send("DELETE", fmt.Sprintf("/users/mappings/%d", id), nil); err != nil {
		return err
	}
	fmt.Printf("deleted rule %d on %s\n", id, c.baseURL)
	return nil
}

func fetchUserRules(c *client) ([]userMapping, error) {
	var rules []userMapping
	if err := c.get("/users/mappings", nil, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// --- mappings ---

func cmdMappings(c *client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: openstatsctl mappings list|set|ignore|unignore")
	}
	switch args[0] {
	case "list":
		return mappingsList(c, args[1:])
	case "set":
		return mappingsSet(c, args[1:])
	case "ignore":
		if len(args) < 2 {
			return fmt.Errorf("usage: openstatsctl mappings ignore <exe-or-display-name>")
		}
		if err := c.send("POST", "/reports/ignore-app", map[string]string{"name": args[1]}); err != nil {
			return err
		}
		fmt.Printf("ignored app %q on %s\n", args[1], c.baseURL)
		return nil
	case "unignore":
		return mappingsUnignore(c, args[1:])
	default:
		return fmt.Errorf("unknown mappings subcommand %q", args[0])
	}
}

func mappingsList(c *client, args []string) error {
	mappings, err := fetchMappings(c)
	if err != nil {
		return err
	}

	onlyIgnored := hasFlag(args, "--ignored")
	onlyReview := hasFlag(args, "--review") // auto-discovered, not yet curated
	filtered := mappings[:0]
	for _, m := range mappings {
		if onlyIgnored && !m.Ignored {
			continue
		}
		if onlyReview && (m.Source != "auto" || m.Ignored) {
			continue
		}
		filtered = append(filtered, m)
	}

	if hasFlag(args, "--json") {
		return emitJSON(filtered)
	}
	t := newTable("EXE", "DISPLAY NAME", "CATEGORY", "SOURCE", "IGNORED")
	for _, m := range filtered {
		t.add(m.ExeName, m.DisplayName, orDash(m.Category), m.Source, yesNo(m.Ignored))
	}
	t.render(os.Stdout)
	return nil
}

func mappingsSet(c *client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: openstatsctl mappings set <exe-name> --name <display> " +
			"[--category X] [--publisher Y] [--family Z]")
	}
	exe := args[0]
	display := flagValue(args, "--name")
	if display == "" {
		return fmt.Errorf("--name <display-name> is required")
	}
	payload := map[string]interface{}{
		"exeName":     exe,
		"displayName": display,
		"category":    flagValue(args, "--category"),
		"publisher":   flagValue(args, "--publisher"),
		"family":      flagValue(args, "--family"),
		"ignored":     false,
	}
	if err := c.send("PUT", "/mappings", payload); err != nil {
		return err
	}
	fmt.Printf("mapped %s -> %q on %s\n", exe, display, c.baseURL)
	return nil
}

func mappingsUnignore(c *client, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: openstatsctl mappings unignore <exe-or-display-name>")
	}
	target := strings.ToLower(strings.TrimSpace(args[0]))
	mappings, err := fetchMappings(c)
	if err != nil {
		return err
	}
	for _, m := range mappings {
		if !m.Ignored {
			continue
		}
		if strings.ToLower(m.ExeName) != target && strings.ToLower(m.DisplayName) != target {
			continue
		}
		if err := c.send("PATCH", fmt.Sprintf("/mappings/%d/ignore", m.ID),
			map[string]bool{"ignored": false}); err != nil {
			return err
		}
		fmt.Printf("un-ignored %s on %s\n", m.ExeName, c.baseURL)
		return nil
	}
	return fmt.Errorf("no ignored mapping matches %q", args[0])
}

func fetchMappings(c *client) ([]softwareMapping, error) {
	var mappings []softwareMapping
	if err := c.get("/mappings", nil, &mappings); err != nil {
		return nil, err
	}
	return mappings, nil
}

// --- reports ---

// reportDef maps a friendly name to its endpoint and the unit of its value.
type reportDef struct {
	path  string
	unit  string
	notes string
}

var reports = map[string]reportDef{
	"top-apps":                  {"/reports/top-apps", "hours", ""},
	"top-apps-by-launches":      {"/reports/top-apps-by-launches", "launches", ""},
	"top-apps-by-foreground":    {"/reports/top-apps-by-foreground", "hours", ""},
	"bottom-apps-by-launches":   {"/reports/bottom-apps-by-launches", "launches", ""},
	"bottom-apps-by-foreground": {"/reports/bottom-apps-by-foreground", "hours", ""},
	"usage-by-lab":              {"/reports/usage-by-lab", "seconds", ""},
	"active-users":              {"/reports/active-users", "active", ""},
	"top-devices-by-sessions":   {"/reports/top-devices-by-sessions", "logins", "logins are sparse; try --range 30d"},
	"top-users-by-logins":       {"/reports/top-users-by-logins", "logins", "logins are sparse; try --range 30d"},
	"top-users-by-session-time": {"/reports/top-users-by-session-time", "hours", ""},
	"avg-session-time":          {"/reports/avg-session-time", "minutes", "needs a login inside the range to divide by"},
	"top-apps-by-elevations":    {"/reports/top-apps-by-elevations", "elevations", "Windows 0.3.0+ / macOS 0.4.0+ agents; sparse — try --range 30d"},
	"top-users-by-elevations":   {"/reports/top-users-by-elevations", "elevations", "Windows 0.3.0+ / macOS 0.4.0+ agents; sparse — try --range 30d"},
}

func cmdReports(c *client, args []string) error {
	if len(args) == 0 || args[0] == "list" {
		names := make([]string, 0, len(reports))
		for name := range reports {
			names = append(names, name)
		}
		sortStrings(names)
		t := newTable("REPORT", "UNIT", "NOTES")
		for _, name := range names {
			t.add(name, reports[name].unit, orDash(reports[name].notes))
		}
		t.render(os.Stdout)
		return nil
	}

	name := args[0]
	def, ok := reports[name]
	if !ok {
		return fmt.Errorf("unknown report %q (try: openstatsctl reports list)", name)
	}

	params := url.Values{}
	for _, flag := range []string{"range", "limit", "lab", "hostname", "start", "end"} {
		if v := flagValue(args, "--"+flag); v != "" {
			params.Set(flag, v)
		}
	}

	var vector promVector
	if err := c.get(def.path, params, &vector); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return emitJSON(vector)
	}
	renderVector(vector, strings.ToUpper(def.unit))
	if len(vector.Data.Result) == 0 && def.notes != "" {
		fmt.Printf("note: %s\n", def.notes)
	}
	return nil
}

// --- settings ---

func cmdSettings(c *client, args []string) error {
	if len(args) == 0 || args[0] != "get" {
		return fmt.Errorf("usage: openstatsctl settings get  (writes are intentionally not supported)")
	}
	var s settings
	if err := c.get("/settings", nil, &s); err != nil {
		return err
	}
	return emitJSON(s)
}

// --- flag helpers ---
//
// Hand-rolled so that flags may appear before or after positional arguments,
// which is how both people and agents actually type them.

func extractServerFlag(args []string) ([]string, string) {
	out := make([]string, 0, len(args))
	server := ""
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--server" && i+1 < len(args):
			server = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--server="):
			server = strings.TrimPrefix(args[i], "--server=")
		default:
			out = append(out, args[i])
		}
	}
	return out, server
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

// flagValue reads --name value or --name=value.
func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"=")
		}
	}
	return ""
}

func derefLab(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func usage() {
	fmt.Print(`openstatsctl — client for a live OpenStats instance

Usage:
  openstatsctl [--server URL] <command> [args] [--json]

Server resolution: --server, then $OPENSTATS_URL, then ` + defaultServer + `

Commands:
  status                                 Fleet overview + server build
  version                                Server version/commit/build date

  agents list [--status online] [--lab X] List agents
  agents get <agent-id>                   Full agent record (JSON)
  agents assign <agent-id> --lab <lab-id> Assign an agent to a lab

  labs list                               List labs

  users list [--range 30d] [--ignored|--tracked]
                                          Users seen in metrics, grouped by identity
  users rules                             Ignore/correlation rules
  users rules rm <rule-id>                Delete a rule
  users policy                            Effective policy pushed to agents
  users ignore <name-or-pattern>          Stop tracking an account (supports *)
  users unignore <name-or-pattern>        Clear an ignore rule
  users alias <pattern> <canonical>       Merge one username into another

  mappings list [--ignored|--review]      Software name mappings
  mappings set <exe> --name <display> [--category X] [--publisher Y] [--family Z]
  mappings ignore <name>                  Drop an app from reports
  mappings unignore <name>                Restore an app

  reports list                            Available reports
  reports <name> [--range 7d] [--limit 10] [--lab X] [--hostname Y]
                                          Run a report

  settings get                            Read fleet settings

Excluded on purpose: deleting agents, forcing agent updates, and writing fleet
settings. Use the web portal for those — they are disruptive and easy to trigger
from a mistaken premise.
`)
}
