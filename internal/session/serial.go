package session

import (
	"errors"
	"fmt"
	"time"

	"go.bug.st/serial"
)

// busyRetryFor bounds how long a just-closed port is waited out. Windows
// hands the handle back a few hundred milliseconds after the previous reader
// goes away, so running the same batch twice in a row used to fail on a port
// nothing was using any more.
const (
	busyRetryFor  = 3 * time.Second
	busyRetryWait = 100 * time.Millisecond
)

// serialSession wraps an open serial port.
type serialSession struct {
	port serial.Port
	name string
}

func (s *serialSession) Read(p []byte) (int, error)  { return s.port.Read(p) }
func (s *serialSession) Write(p []byte) (int, error) { return s.port.Write(p) }
func (s *serialSession) Close() error                { return s.port.Close() }

// LineEnding is a bare CR on a console line.
//
// Telnet and SSH carry CRLF, but a serial console has no protocol between the
// keyboard and the device's line discipline: it takes CR and LF each as their
// own Enter. Sending "admin\r\n" therefore submits the username on the CR and
// an empty line on the LF — which the device answers at the password prompt,
// so every console login failed with "Login attempt failed." while the same
// credentials worked over the network.
func (s *serialSession) LineEnding() string { return "\r" }

// dialSerial opens a COM port. If name is empty, the first available port is
// used (mirrors the legacy tool's COM auto-detection).
func dialSerial(name string, baud int) (Session, error) {
	if name == "" {
		detected, err := autoDetectPort()
		if err != nil {
			return nil, err
		}
		name = detected
	}
	mode := &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
	port, err := openBusyAware(name, mode)
	if err != nil {
		return nil, fmt.Errorf("open serial %s @ %d: %w", name, baud, err)
	}
	return &serialSession{port: port, name: name}, nil
}

// openBusyAware opens the port, waiting out a "busy" that is only the
// previous session letting go.
//
// Closing a port does not return the handle instantly: measured on this bench
// it takes about 400ms. A second run started sooner than that — clicking 実行
// again right after the first finished — failed with "Serial port busy" even
// though nothing else had the port. Only PortBusy is retried, so a wrong COM
// name still fails immediately instead of hanging for seconds.
func openBusyAware(name string, mode *serial.Mode) (serial.Port, error) {
	deadline := time.Now().Add(busyRetryFor)
	for {
		port, err := serial.Open(name, mode)
		if err == nil {
			return port, nil
		}
		var pe *serial.PortError // the library returns the pointer form
		if !errors.As(err, &pe) || pe.Code() != serial.PortBusy || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(busyRetryWait)
	}
}

// ListSerialPorts returns the COM ports currently present (for the GUI).
func ListSerialPorts() ([]string, error) {
	return serial.GetPortsList()
}

func autoDetectPort() (string, error) {
	ports, err := serial.GetPortsList()
	if err != nil {
		return "", err
	}
	if len(ports) == 0 {
		return "", fmt.Errorf("no serial ports found")
	}
	return ports[0], nil
}
