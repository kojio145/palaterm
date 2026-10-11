package session

import (
	"strings"
	"testing"
)

// The field PC: a built-in COM1 with nothing on it and the USB console
// cable on COM3. The cable must win, whatever the list order.
func TestPickPortPrefersTheOneUSBAdapter(t *testing.T) {
	got, err := pickPort([]portInfo{{Name: "COM1"}, {Name: "COM3", USB: true}})
	if err != nil || got != "COM3" {
		t.Fatalf("got %q, %v; want COM3", got, err)
	}
}

// Two console cables: no way to know which device is on which, so the
// choice has to be made in the device editor. The error names both.
func TestPickPortRefusesTwoUSBAdapters(t *testing.T) {
	_, err := pickPort([]portInfo{{Name: "COM1"}, {Name: "COM3", USB: true}, {Name: "COM4", USB: true}})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"COM3", "COM4", "COMポートを指定"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "COM1") {
		t.Errorf("the onboard port is not a candidate; error %q", err)
	}
}

// A desktop with one real RS-232 port and no adapter: the one port is the
// only possibility, so it is used.
func TestPickPortUsesTheOnlyPortWhenNoUSB(t *testing.T) {
	got, err := pickPort([]portInfo{{Name: "COM1"}})
	if err != nil || got != "COM1" {
		t.Fatalf("got %q, %v; want COM1", got, err)
	}
}

// Several onboard ports and no adapter: ambiguous, refuse and list them.
func TestPickPortRefusesSeveralOnboardPorts(t *testing.T) {
	_, err := pickPort([]portInfo{{Name: "COM1"}, {Name: "COM2"}})
	if err == nil || !strings.Contains(err.Error(), "COM1, COM2") {
		t.Fatalf("got %v; want an error naming COM1, COM2", err)
	}
}

func TestPickPortNoPorts(t *testing.T) {
	if _, err := pickPort(nil); err == nil {
		t.Fatal("expected an error with no ports")
	}
}
