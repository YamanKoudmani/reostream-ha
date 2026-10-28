//go:build race

package control

// raceEnabled is true when the tests run under -race, where timings are
// slowed many times over and timing assertions mean nothing.
const raceEnabled = true
