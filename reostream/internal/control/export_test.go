package control

// The tests' fake cameras script one connection's replies; the camera
// header's identity read would consume them. See skipIdent.
func init() { skipIdent = true }
