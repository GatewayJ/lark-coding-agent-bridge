//go:build !race

package bridge

func bridgeTestRaceEnabled() bool {
	return false
}
