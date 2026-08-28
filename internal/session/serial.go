package session

import (
	"fmt"

	"go.bug.st/serial"
)

// serialSession wraps an open serial port.
type serialSession struct {
	port serial.Port
	name string
}

func (s *serialSession) Read(p []byte) (int, error)  { return s.port.Read(p) }
func (s *serialSession) Write(p []byte) (int, error) { return s.port.Write(p) }
func (s *serialSession) Close() error                { return s.port.Close() }

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
	port, err := serial.Open(name, mode)
	if err != nil {
		return nil, fmt.Errorf("open serial %s @ %d: %w", name, baud, err)
	}
	return &serialSession{port: port, name: name}, nil
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
