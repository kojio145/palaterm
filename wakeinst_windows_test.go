package main

import "testing"

// readPowerInfo answers on this machine (values depend on the PC; this
// checks the calls work and the setting reads as one of its known values).
func TestReadPowerInfo(t *testing.T) {
	pi := readPowerInfo()
	t.Logf("%+v", pi)
	for _, v := range []int{pi.WakeTimersAC, pi.WakeTimersDC} {
		if v < 0 || v > 2 {
			t.Errorf("wake timer setting not read: %+v", pi)
		}
	}
}
