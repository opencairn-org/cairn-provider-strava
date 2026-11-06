package mockstrava

import "regexp"

// loopback matches the only URLs the mock redirects to or calls: http(s) on a loopback host.
var loopback = regexp.MustCompile(`^https?://(127\.0\.0\.1|localhost|\[::1\])(:[0-9]+)?([/?#]|$)`)

// verifyToken matches the push-subscription verify tokens the mock sends back: URL-safe, short.
var verifyToken = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,128}$`)
