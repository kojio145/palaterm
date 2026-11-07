package guard

import "testing"

// Review 2026-11-01 (B S-1, H 型1, B B-4): the vendors' words for entering
// a configuration mode, rebooting, erasing and changing settings that the
// rules did not know, and the reads that they flagged. Both directions are
// pinned. Every value is a lab one (192.0.2.x, lab-sw1).
func TestReview1101FlagsVendorChanges(t *testing.T) {
	cases := map[string]string{
		// Comware / VRP
		"system-view":                          ReasonConfigMode,
		"sys":                                  ReasonConfigMode,
		"undo shutdown":                        ReasonInterface,
		"sysname lab-sw1":                      ReasonInterface,
		"ip route-static 0.0.0.0 0 192.0.2.1":  ReasonInterface,
		"vlan batch 10 20":                     ReasonInterface,
		"startup saved-configuration x.cfg":    ReasonErase,
		"startup system-software x.bin":        ReasonErase,
		"restore startup-configuration from x": ReasonErase,
		"boot-loader file flash:/x.bin":        ReasonErase,
		"fixdisk flash:":                       ReasonErase,
		"rmdir flash:/x":                       ReasonErase,
		"schedule reboot at 22:00":             ReasonReload,
		"reset saved-configuration":            ReasonErase,
		// PAN-OS
		"request restart system":                  ReasonReload,
		"request shutdown system":                 ReasonReload,
		"request system private-data-reset":       ReasonReload,
		"request high-availability state suspend": ReasonReload,
		"load config from x.xml":                  ReasonErase,
		// Junos
		"request vmhost reboot":                      ReasonReload,
		"request chassis fpc slot 0 restart":         ReasonReload,
		"request system storage cleanup":             ReasonErase,
		"request system configuration rescue delete": ReasonErase,
		"load factory-default":                       ReasonErase,
		"load override x.conf":                       ReasonErase,
		"load replace x.conf":                        ReasonErase,
		"load merge x.conf":                          ReasonErase,
		"delete":                                     ReasonErase,
		"start shell":                                ReasonConfigMode,
		"run request system reboot":                  ReasonReload,
		// FortiOS
		"purge":                                     ReasonErase,
		"execute log delete-all":                    ReasonErase,
		"execute cfg reload":                        ReasonErase,
		"execute cfg save":                          ReasonWrite,
		"execute ha failover set 1":                 ReasonReload,
		"execute router restart":                    ReasonReload,
		"execute disconnect-admin-session 1":        ReasonReload,
		"execute wireless-controller reset-wtp all": ReasonReload,
		"execute factoryreset":                      ReasonReload,
		// Cisco IOS / XE / XR / NX-OS / ASA
		"default interface GigabitEthernet0/1":                                  ReasonErase,
		"hw-module module 1 reset":                                              ReasonReload,
		"hw-module location 0/0/CPU0 reload":                                    ReasonReload,
		"admin reload location all":                                             ReasonReload,
		"redundancy force-switchover":                                           ReasonReload,
		"redundancy reload peer":                                                ReasonReload,
		"request platform software package install switch all file flash:x.bin": ReasonInterface,
		"system switchover":                                                     ReasonReload,
		"failover active":                                                       ReasonReload,
		"failover reload-standby":                                               ReasonReload,
		"write standby":                                                         ReasonWrite,
		"clear ip bgp *":                                                        ReasonInterface,
		"clear ip ospf process":                                                 ReasonInterface,
		"clear xlate":                                                           ReasonInterface,
		"debug all":                                                             ReasonInterface,
		"feature bgp":                                                           ReasonInterface,
		// Yamaha RTX
		"ip lan1 address 192.0.2.1/24":     ReasonInterface,
		"ip pp address 192.0.2.2/32":       ReasonInterface,
		"ip tunnel1 address 192.0.2.3/32":  ReasonInterface,
		"ip wan1 address dhcp":             ReasonInterface,
		"pp select 1":                      ReasonInterface,
		"pp disable 1":                     ReasonInterface,
		"pp enable 1":                      ReasonInterface,
		"tunnel select 1":                  ReasonInterface,
		"tunnel enable 1":                  ReasonInterface,
		"tunnel disable 1":                 ReasonInterface,
		"ip filter 100 reject * * * * *":   ReasonInterface,
		"nat descriptor type 1 masquerade": ReasonInterface,
		"dhcp scope 1 192.0.2.0/24":        ReasonInterface,
		"dhcp service server":              ReasonInterface,
		"vlan lan1/1 802.1q vid=10":        ReasonInterface,
		"login password":                   ReasonInterface,
		"administrator password":           ReasonConfigMode, // "administrator" is the mode word
		"http revision-up go":              ReasonInterface,
		"copy config 0 1":                  ReasonWrite,
		"default config":                   ReasonErase,
		"cold start":                       ReasonErase,
		// AlliedWare Plus, EOS, VyOS, ArubaOS-Switch, NetScreen, Extreme
		"boot config-file flash:/x.cfg": ReasonInterface,
		"atmf reboot-rolling":           ReasonReload,
		"bash":                          ReasonConfigMode,
		"bash sudo reboot":              ReasonConfigMode,
		"add system image x.iso":        ReasonInterface,
		"load /config/x.boot":           ReasonErase,
		"run reboot":                    ReasonReload,
		"boot":                          ReasonReload,
		"reset":                         ReasonReload,
		"unconfigure switch all":        ReasonErase,
		"disable port 1":                ReasonInterface,
		// Linux (the linux / linux-root profiles)
		"dd if=x of=y":                      ReasonErase,
		"mkfs.ext4 /dev/sdb1":               ReasonErase,
		"mkfs -t ext4 /dev/sdb1":            ReasonErase,
		"wipefs -a /dev/sdb":                ReasonErase,
		"shred -u x":                        ReasonErase,
		"fdisk /dev/sdb":                    ReasonErase,
		"parted /dev/sdb":                   ReasonErase,
		"mv /etc/hosts /tmp/":               ReasonErase,
		"truncate -s 0 x.log":               ReasonErase,
		"crontab -r":                        ReasonErase,
		"shutdown 0":                        ReasonReload,
		"shutdown 20:00":                    ReasonReload,
		"shutdown --poweroff":               ReasonReload,
		"shutdown --reboot now":             ReasonReload,
		"systemctl stop nginx":              ReasonInterface,
		"systemctl restart nginx":           ReasonInterface,
		"systemctl disable nginx":           ReasonInterface,
		"systemctl mask nginx":              ReasonInterface,
		"systemctl isolate rescue":          ReasonInterface,
		"service nginx stop":                ReasonInterface,
		"service nginx restart":             ReasonInterface,
		"ifdown eth0":                       ReasonInterface,
		"ip link set eth0 down":             ReasonInterface,
		"ip addr flush dev eth0":            ReasonInterface,
		"ip addr del 192.0.2.1/24 dev eth0": ReasonInterface,
		"ip addr add 192.0.2.1/24 dev eth0": ReasonInterface,
		"iptables -F":                       ReasonInterface,
		"iptables -P INPUT DROP":            ReasonInterface,
		"nft flush ruleset":                 ReasonInterface,
		"ufw disable":                       ReasonInterface,
		"ufw reset":                         ReasonInterface,
		"ufw enable":                        ReasonInterface,
		"kill -9 1234":                      ReasonInterface,
		"killall nginx":                     ReasonInterface,
		"pkill nginx":                       ReasonInterface,
		"apt remove nginx":                  ReasonInterface,
		"apt-get purge nginx":               ReasonInterface,
		"yum install nginx":                 ReasonInterface,
		"dnf upgrade":                       ReasonInterface,
		"pip install x":                     ReasonInterface,
		"pip3 install x":                    ReasonInterface,
		"userdel ops2":                      ReasonErase,
		"passwd ops":                        ReasonInterface,
		"chmod -R 777 /srv":                 ReasonInterface,
		"chown -R ops /srv":                 ReasonInterface,
		"docker rm -f web":                  ReasonInterface,
		"docker rmi web":                    ReasonInterface,
		"docker system prune -f":            ReasonInterface,
		// shell redirection, anywhere on the line
		"echo 192.0.2.1 lab-sw1 > /etc/hosts":  ReasonWrite,
		"echo 192.0.2.1 lab-sw1 >> /etc/hosts": ReasonWrite,
		"cat x.cfg | tee /etc/x.cfg":           ReasonWrite,
		": > /var/log/x.log":                   ReasonWrite,
		// run-as prefixes with options
		"sudo -i reboot":                 ReasonReload,
		"sudo -S reboot":                 ReasonReload,
		"sudo -u root reboot":            ReasonReload,
		"sudo -H systemctl reboot":       ReasonReload,
		"doas reboot":                    ReasonReload,
		"su -c reboot":                   ReasonReload,
		"su -c \"systemctl stop nginx\"": ReasonInterface,
		"sudo su -c reboot":              ReasonReload,
		"Run reboot":                     ReasonReload,
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

// The reads the old rules flagged, and the look-alikes the word boundaries
// must keep apart. A pager-off line must never warn.
func TestReview1101PassesReads(t *testing.T) {
	safe := []string{
		// Comware / VRP / VyOS exec-level resets
		"reset counters", "reset counters interface GigabitEthernet1/0/1", "reset arp all",
		"reset logbuffer", "reset ip bgp 192.0.2.1",
		// pager-off lines a set may spell out
		"set cli screen-length 0", "set cli pager off", "set terminal length 0",
		"set console page 0", "set terminal pager disable", "no page",
		"terminal length 0", "screen-length disable", "console lines infinity",
		"terminal pager 0", "export PAGER=cat SYSTEMD_PAGER=cat",
		// Linux reads
		"ip route show", "ip route list", "ip route get 192.0.2.1", "ip addr show", "ip link show",
		"systemctl status nginx", "service nginx status", "docker ps", "apt list --installed",
		"pip list", "cat /etc/hosts 2>/dev/null", "ls -l > /dev/null", "sudo -u ops id",
		"iptables-save | head", "journalctl -u nginx", "crontab -l", "chmod 644 x", "df -h",
		// word boundaries
		"reload cancel", "shutdown -c", "restarting", "haltx", "rebooted", "reloaded",
		"show reload", "show redundancy", "show failover", "show startup-config",
		"show system", "show feature", "display startup", "display boot-loader",
		"request support information", "show chassis hardware",
		"display ip routing-table", "show ip route 192.0.2.0",
		// a shell's own reads
		"run show version", "do show run", "sudo cat /var/log/syslog",
	}
	if hits := Check(safe); len(hits) != 0 {
		t.Errorf("safe commands flagged: %+v", hits)
	}
}

// strip takes the run-as prefixes and nothing else.
func TestReview1101StripPrefixes(t *testing.T) {
	for in, want := range map[string]string{
		"sudo -u root reboot": "reboot",
		"sudo -i reboot":      "reboot",
		"sudo reboot":         "reboot",
		"sudo":                "sudo",
		"do show run":         "show run",
		"run show version":    "show version",
		"su -c 'reboot'":      "reboot'",
		"doas -u ops id":      "id", // "-u ops" is doas's too, but doas is stripped bare
		"runbook":             "runbook",
		"sudoers":             "sudoers",
		"document":            "document",
	} {
		if got := strip(in); got != want {
			t.Errorf("strip(%q) = %q, want %q", in, got, want)
		}
	}
}
