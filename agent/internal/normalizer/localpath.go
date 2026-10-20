package normalizer

// isLocalDrivePath reports whether p is an absolute path on a lettered drive
// (`X:\...` or `\\?\X:\...`) and returns the drive root (`X:\`). UNC, WebDAV,
// device (`\\.\`), `\\?\UNC\`, forward-slash network and relative paths are
// rejected so no file API ever touches a network location as SYSTEM.
func isLocalDrivePath(p string) (root string, ok bool) {
	const verbatim = `\\?\`
	if len(p) >= len(verbatim) && p[:len(verbatim)] == verbatim {
		p = p[len(verbatim):]
	}
	if len(p) < 3 {
		return "", false
	}
	c := p[0]
	if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
		return "", false
	}
	if p[1] != ':' || (p[2] != '\\' && p[2] != '/') {
		return "", false
	}
	// Reject embedded NULs and any further prefix tricks.
	for i := 0; i < len(p); i++ {
		if p[i] == 0 {
			return "", false
		}
	}
	return string(c) + `:\`, true
}
