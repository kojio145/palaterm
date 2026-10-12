package guard

import "testing"

func TestCheckFlagsChanges(t *testing.T) {
	cases := map[string]string{
		"conf t":                             ReasonConfigMode,
		"configure terminal":                 ReasonConfigMode,
		"configure":                          ReasonConfigMode,
		"config system interface":            ReasonConfigMode,
		"svintr-config":                      ReasonConfigMode,
		"wr":                                 ReasonWrite,
		"write memory":                       ReasonWrite,
		"copy running-config startup-config": ReasonWrite,
		"save":                               ReasonWrite,
		"reload":                             ReasonReload,
		"reload in 5":                        ReasonReload,
		"execute reboot":                     ReasonReload,
		"request system reboot":              ReasonReload,
		"erase startup-config":               ReasonErase,
		"write erase":                        ReasonErase,
		"delete flash:old.bin":               ReasonErase,
		"no shutdown":                        ReasonInterface,
		"interface GigabitEthernet0/1":       ReasonInterface,
		"set system host-name r1":            ReasonInterface,
		"ip route 0.0.0.0 0.0.0.0 10.0.0.1":  ReasonInterface,
		"commit":                             ReasonCommit,
		"do reload":                          ReasonReload,
		// IOS short forms (answered [confirm] would run them unwarned).
		"rel":                                ReasonReload,
		"relo":                               ReasonReload,
		"del flash:x.bin":                    ReasonErase,
		"dele flash:x.bin":                   ReasonErase,
		"era startup-config":                 ReasonErase,
		"form flash:":                        ReasonErase,
		"wri":                                ReasonWrite,
		"wri mem":                            ReasonWrite,
		"wr me":                              ReasonWrite,
		"copy run start":                     ReasonWrite,
		"cop run start":                      ReasonWrite,
		"copy tftp: running-config":          ReasonWrite,
		"wr erase":                           ReasonErase,
		"wr er":                              ReasonErase,
		"write er":                           ReasonErase,
		"squeeze flash:":                     ReasonErase,
		"copy run sta":                       ReasonWrite,
		"copy ru st":                         ReasonWrite,
		"copy flash:a.bin flash:b.bin":       ReasonWrite,
		"copy tftp://h/c3560.bin bootflash:": ReasonWrite,
		"archive download-sw /reload tftp://h/i.tar": ReasonWrite,
		"Do reload":        ReasonReload,
		"sudo reboot":      ReasonReload,
		"shutdown -r now":  ReasonReload,
		"shutdown -h now":  ReasonReload,
		"sudo init 6":      ReasonReload,
		"systemctl reboot": ReasonReload,
	}
	for cmd, want := range cases {
		hits := Check([]string{cmd})
		if len(hits) != 1 {
			t.Errorf("%q: want 1 hit, got %d", cmd, len(hits))
			continue
		}
		if hits[0].Reason != want {
			t.Errorf("%q: reason %q, want %q", cmd, hits[0].Reason, want)
		}
	}
}

func TestCheckPassesReads(t *testing.T) {
	safe := []string{
		"show running-config", "show startup-config", "show clock", "show interfaces status",
		"show ip route", "show version", "get system status", "diagnose sys ntp status",
		"show configuration", "show config", "terminal length 0", "do show run",
		"display current-configuration", "show tech-support", "ping 10.0.0.1",
		"show interfaces", "show vlan", "show arp neighbors", "exit", "",
		"get router info routing-table all", "show system interface | grep -i edit",
		// look-alikes of the IOS short forms that only read
		"write terminal", "wr t", "copy run tftp://192.0.2.10/r1.cfg", "copy startup-config tftp:",
		"display", "release", "relay",
		"copy flash:x.cfg tftp://h/x.cfg", "copy run tftp:", "sudo systemctl status nginx", "init",
		"show archive", "dir flash:", "sudo cat /etc/hosts",
	}
	if hits := Check(safe); len(hits) != 0 {
		t.Errorf("safe commands flagged: %+v", hits)
	}
}

func TestCheckKeepsOrderAndIndex(t *testing.T) {
	hits := Check([]string{"show clock", "conf t", "hostname x", "end", "wr"})
	if len(hits) != 3 || hits[0].Index != 1 || hits[1].Index != 2 || hits[2].Index != 4 {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}
