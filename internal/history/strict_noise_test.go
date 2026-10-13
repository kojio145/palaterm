package history

import (
	"reflect"
	"testing"
)

// Typos the device refused, "?" help and completion redraws are not settings
// (user report 2026-10-13: they showed up as "not reflected").
func TestExtractSkipsRefusedAndRedrawnLines(t *testing.T) {
	work := "R1#shwo ip int br\r\n" +
		"        ^\r\n" +
		"% Invalid input detected at '^' marker.\r\n" +
		"\r\n" +
		"R1#show ip int br\r\n" +
		"Interface  IP-Address  OK? Method Status Protocol\r\n" +
		"R1#conf t\r\n" +
		"Enter configuration commands, one per line.  End with CNTL/Z.\r\n" +
		"R1(config)#interface Gi0/1\r\n" +
		"R1(config-if)#de\r\n" + // "de" + Enter
		"% Ambiguous command:  \"de\"\r\n" +
		"R1(config-if)#de?\r\n" + // help
		"default  delay  description\r\n" +
		"\r\n" +
		"R1(config-if)#de\r\n" + // redrawn after the help, then Tab
		"delay  description\r\n" +
		"R1(config-if)#description uplink to core\r\n" + // the real command
		"R1(config-if)#ip address 192.0.2.1 255.255.255.0\r\n" +
		"R1(config-if)#ip address 192.0.2.1 255.255.255.0 secondary\r\n" + // extends the previous one: still a setting
		"R1(config-if)#end\r\n" +
		"R1#\r\n"
	got, _ := ExtractConfigCommands(work)
	want := []string{
		"interface Gi0/1",
		"description uplink to core",
		"ip address 192.0.2.1 255.255.255.0",
		"ip address 192.0.2.1 255.255.255.0 secondary",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}

	// NEC IX: an ambiguous Tab lists the candidates and redraws the line.
	ix := "IX-A(config)# interface GigaEthernet0.0\r\n" +
		"IX-A(config-GigaEthernet0.0)# de\r\n" +
		"  description  delay\r\n" +
		"IX-A(config-GigaEthernet0.0)# description wan\r\n" +
		"IX-A(config-GigaEthernet0.0)# sh ip route\r\n" +
		"% sh -- Invalid command.\r\n"
	got, _ = ExtractConfigCommands(ix)
	want = []string{"interface GigaEthernet0.0", "description wan"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IX: got %q, want %q", got, want)
	}

	// End to end: none of the noise is reported as missing.
	after := "interface Gi0/1\n description uplink to core\n ip address 192.0.2.1 255.255.255.0\n ip address 192.0.2.1 255.255.255.0 secondary\n"
	res := StrictVerify(work, "", after)
	if res.Missing != 0 {
		t.Errorf("missing = %d: %+v", res.Missing, res.Commands)
	}
}
