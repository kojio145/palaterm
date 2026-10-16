package model

import "testing"

func TestGUIURL(t *testing.T) {
	cases := []struct {
		d    Device
		want string
	}{
		{Device{Host: "192.0.2.1"}, "https://192.0.2.1/"},
		{Device{Host: "192.0.2.1", GUIPort: 10443}, "https://192.0.2.1:10443/"},
		{Device{Host: "192.0.2.1", GUIScheme: "http"}, "http://192.0.2.1/"},
		{Device{Host: "192.0.2.1", GUIScheme: "http", GUIPort: 8080}, "http://192.0.2.1:8080/"},
		{Device{Host: "192.0.2.1", GUIScheme: "http", GUIPort: 443}, "http://192.0.2.1:443/"},
		{Device{Host: "2001:db8::1"}, "https://[2001:db8::1]/"},
	}
	for _, c := range cases {
		if got := c.d.GUIURL(); got != c.want {
			t.Errorf("%+v: %s, want %s", c.d, got, c.want)
		}
	}
	d := Device{Name: "fw", Host: "192.0.2.1", Conn: ConnSSH, GUIScheme: "ftp"}
	if err := d.Validate(); err == nil {
		t.Error("scheme ftp accepted")
	}
}
