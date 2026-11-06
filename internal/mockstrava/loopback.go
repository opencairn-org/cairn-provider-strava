package mockstrava

import "regexp"

// loopback matches the only URLs the mock redirects to or calls: http(s) on a loopback host.
var loopback = regexp.MustCompile(`^https?://(127\.0\.0\.1|localhost|\[::1\])(:[0-9]+)?([/?#]|$)`)
