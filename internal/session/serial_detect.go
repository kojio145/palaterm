package session

import (
	"fmt"
	"sort"
	"strings"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// portInfo is what port selection needs to know about one COM port.
type portInfo struct {
	Name string
	USB  bool // a USB-to-serial adapter, as opposed to a port on the motherboard
}

// autoDetectPort picks the COM port for a device whose port field is empty.
//
// It used to take the first name in the list, which on a laptop with a
// built-in COM1 and a USB console cable on COM3 is the wrong one every
// time: COM1 has nothing on it, so the console stayed silent and the user
// could not tell why (2026-10-11, the first serial try on the field PC).
// Now the console cable wins when there is exactly one, and anything
// ambiguous — two cables, or no cable and several onboard ports — is
// refused with the candidates named, so the port gets chosen on purpose
// in the device editor instead of guessed at.
func autoDetectPort() (string, error) {
	ports, err := listPortsDetailed()
	if err != nil {
		return "", err
	}
	return pickPort(ports)
}

// listPortsDetailed enumerates the COM ports with their USB-ness. When the
// detailed enumeration is unavailable, every port counts as non-USB and the
// plain list decides.
func listPortsDetailed() ([]portInfo, error) {
	if det, err := enumerator.GetDetailedPortsList(); err == nil {
		out := make([]portInfo, 0, len(det))
		for _, p := range det {
			out = append(out, portInfo{Name: p.Name, USB: p.IsUSB})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out, nil
	}
	names, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}
	out := make([]portInfo, 0, len(names))
	for _, n := range names {
		out = append(out, portInfo{Name: n})
	}
	return out, nil
}

// pickPort is the pure selection rule: exactly one USB adapter → that one;
// no adapter and exactly one port of any kind → that one; otherwise the
// choice is not obvious and the caller must name the port.
func pickPort(ports []portInfo) (string, error) {
	if len(ports) == 0 {
		return "", fmt.Errorf("no serial ports found — COMポートが見つかりません。USBシリアル変換ケーブルの接続を確認してください")
	}
	var usb []string
	names := make([]string, 0, len(ports))
	for _, p := range ports {
		names = append(names, p.Name)
		if p.USB {
			usb = append(usb, p.Name)
		}
	}
	switch {
	case len(usb) == 1:
		return usb[0], nil
	case len(usb) == 0 && len(ports) == 1:
		return ports[0].Name, nil
	case len(usb) > 1:
		return "", fmt.Errorf("USBシリアル変換が %d 本あります（%s）。機器の編集でCOMポートを指定してください", len(usb), strings.Join(usb, ", "))
	default:
		return "", fmt.Errorf("USBシリアル変換が見つからず、COMポートが複数あります（%s）。機器の編集でCOMポートを指定してください", strings.Join(names, ", "))
	}
}
