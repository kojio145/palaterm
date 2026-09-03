package profile

// Default profiles mirror the 14 OS types handled by the legacy TTL macro's
// changeAccessAuthority routine, written as the same plain wait strings the
// macro used (e.g. wait 'assword:' / wait '#'). No regular expressions:
// every Expect/Prompt is a literal substring, quoted via Quote when the
// profile is registered. Partial spellings like "sername:" / "ogin:" /
// "assword:" match both capitalizations, the classic TTL trick.
//
// Login steps that send {user}/{password} apply only where the device itself
// prompts for them (Telnet/serial, or through a bastion): the runner skips
// them outright over direct SSH and skips any auth step whose prompt never
// appears, so the same profile works over every transport.
//
// These are seed data: they are copied into the vault on creation (and once
// into pre-existing vaults), where the user can edit, duplicate, and delete
// them like any custom profile.

var defaults = []Profile{
	{
		Key:    "cisco-ios",
		Name:   "Cisco IOS",
		Prompt: "#",
		Login: []Step{
			{Expect: "sername:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
			{Expect: ">", Send: "enable"},
			{Expect: "assword:", Send: "{enable}"},
		},
		Pager:      []Step{{Send: "terminal length 0"}},
		Disconnect: []Step{{Send: "exit"}},
	},
	{
		Key:    "cisco-asa",
		Name:   "Cisco ASA",
		Prompt: "#",
		Login: []Step{
			{Expect: "sername:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
			{Expect: ">", Send: "enable"},
			{Expect: "assword:", Send: "{enable}"},
		},
		Pager:      []Step{{Send: "terminal pager 0"}},
		Disconnect: []Step{{Send: "exit"}},
	},
	{
		Key:        "juniper-junos",
		Name:       "Juniper JUNOS",
		Prompt:     ">",
		MorePrompt: "---(more",
		Login: []Step{
			{Expect: "ogin:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
		},
		Pager:      []Step{{Send: "set cli screen-length 0"}},
		Disconnect: []Step{{Send: "exit"}},
	},
	{
		Key:        "juniper-netscreen",
		Name:       "Juniper NetScreen (ScreenOS/SSG)",
		Prompt:     "->",
		MorePrompt: "--- more ---",
		Login: []Step{
			{Expect: "ogin:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
		},
		Pager:      []Step{{Send: "set console page 0"}},
		Disconnect: []Step{{Send: "exit"}},
	},
	{
		Key:    "a10-acos",
		Name:   "A10 ACOS",
		Prompt: "#",
		Login: []Step{
			{Expect: "sername:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
			{Expect: ">", Send: "enable"},
			{Expect: "assword:", Send: "{enable}"},
		},
		Pager: []Step{{Send: "terminal length 0"}},
		Disconnect: []Step{
			{Send: "exit"},
			{Expect: ">", Send: "exit"},
			{Expect: "[y", Send: "y"},
		},
	},
	{
		Key:    "nec-qx",
		Name:   "NEC QX",
		Prompt: "]",
		Login: []Step{
			{Expect: "sername:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
			{Expect: ">", Send: "system-view"},
		},
		Pager:      []Step{{Send: "screen-length disable"}},
		Disconnect: []Step{{Send: "quit"}},
	},
	{
		Key:    "fortinet-fortios",
		Name:   "Fortinet FortiGate",
		Prompt: "#",
		Login: []Step{
			{Expect: "ogin:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
		},
		Pager: []Step{
			{Send: "config system console"},
			{Send: "set output standard"},
			{Send: "end"},
		},
		Disconnect: []Step{{Send: "exit"}},
	},
	{
		Key:    "fortinet-analyzer",
		Name:   "Fortinet FortiAnalyzer",
		Prompt: "#",
		Login: []Step{
			{Expect: "ogin:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
		},
		Pager: []Step{
			{Send: "config system console"},
			{Send: "set output standard"},
			{Send: "end"},
		},
		Disconnect: []Step{{Send: "exit"}},
	},
	{
		Key:    "hpe-comware",
		Name:   "HPE (Comware)",
		Prompt: "]",
		Login: []Step{
			{Expect: "sername:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
			{Expect: ">", Send: "screen-length disable"},
			{Expect: ">", Send: "system-view"},
		},
		Disconnect: []Step{
			{Send: "quit"},
			{Expect: ">", Send: "quit"},
		},
	},
	{
		// NEC IX puts "terminal length 0" — and "show running-config" — behind
		// configure mode: in operation mode the device answers
		// "% terminal -- Invalid command." and keeps paging. So the pager step
		// enters configure first and stays there, which is how IX is operated
		// (nothing is written without an explicit "write memory").
		//
		// It enters with "svintr-config" rather than "configure" because only
		// one session may hold configure mode: "configure" fails outright with
		// "% CONFIG process is occupied." when someone else is already in it,
		// which would silently cost the pager and every show running-config of
		// the batch. svintr-config takes it (dropping that session back to
		// operation mode) — deliberate, and the reason it is worth knowing that
		// a run can bump a colleague out of configure mode. Swap it for
		// "configure" in OSタイプ設定 to run strictly non-intrusively.
		//
		// MorePrompt is the net for when neither works (a monitor-privilege
		// account, or firmware without svintr-config): the run still completes,
		// answering each --More-- with a space.
		Key:        "nec-ix",
		Name:       "NEC IX",
		Prompt:     "#",
		MorePrompt: "--More--",
		// No privilege-escalation row: IX has no ">" level to escalate FROM.
		// An administrator lands on "#" and a monitor user on "%", and "enable"
		// on IX does not mean "become privileged" at all — it enters config
		// mode, which the pager rows below already do properly. The row was
		// inherited from the Cisco profiles and only ever cost a timeout.
		Login: []Step{
			{Expect: "ogin:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
		},
		Pager: []Step{
			{Send: "svintr-config"},
			{Send: "terminal length 0"},
		},
		Disconnect: []Step{{Send: "exit"}, {Send: "exit"}},
	},
	{
		Key:    "nec-wa",
		Name:   "NEC WA (Agater)",
		Prompt: "#",
		Login: []Step{
			{Expect: "ogin:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
			{Expect: ">", Send: "enable"},
		},
		Pager:      []Step{{Send: "terminal length 0"}},
		Disconnect: []Step{{Send: "exit"}},
	},
	{
		Key:    "aruba-os-switch",
		Name:   "Aruba ArubaOS-Switch",
		Prompt: "#",
		Login: []Step{
			{Expect: "sername:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
			{Expect: "#", Enter: true},
		},
		Pager:      []Step{{Send: "no page"}},
		Disconnect: []Step{{Send: "exit"}},
	},
	{
		Key:    "yamaha-rtx",
		Name:   "Yamaha RTX",
		Prompt: "#",
		Login: []Step{
			{Expect: ">", Send: "administrator"},
			{Expect: "assword:", Send: "{enable}"},
			{Expect: "#", Send: "console character ja.utf8"},
		},
		Pager:      []Step{{Send: "console lines infinity"}},
		Disconnect: []Step{{Send: "exit"}},
	},
	{
		Key:    "generic",
		Name:   "Generic (no automation)",
		Prompt: "#",
		// Two user waits cover both prompt spellings ("login:" and
		// "Username:") — plain text has no alternation, so the one that never
		// appears is simply skipped after the auth-step short wait.
		Login: []Step{
			{Expect: "ogin:", Send: "{user}"},
			{Expect: "sername:", Send: "{user}"},
			{Expect: "assword:", Send: "{password}"},
		},
		Disconnect: []Step{{Send: "exit"}},
	},
}

// Defaults returns deep copies of the default profiles, safe for the caller
// to store and mutate (they become editable vault data).
func Defaults() []Profile {
	out := make([]Profile, len(defaults))
	for i, p := range defaults {
		out[i] = copyProfile(p)
	}
	return out
}

func copyProfile(p Profile) Profile {
	q := p
	q.Login = append([]Step(nil), p.Login...)
	q.Pager = append([]Step(nil), p.Pager...)
	q.Disconnect = append([]Step(nil), p.Disconnect...)
	return q
}

// Registry indexes profiles by key. It starts empty; the app registers the
// vault's profiles (defaults seeded at creation plus user-made ones), quoted
// via Quote.
type Registry struct {
	byKey map[string]Profile
	order []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byKey: make(map[string]Profile)}
}

// Get returns the profile for key, falling back to the registered "generic"
// profile, or to a minimal hardcoded one when even that has been deleted.
func (r *Registry) Get(key string) Profile {
	if p, ok := r.byKey[key]; ok {
		return p
	}
	if p, ok := r.byKey["generic"]; ok {
		return p
	}
	for _, d := range defaults {
		if d.Key == "generic" {
			return Quote(copyProfile(d))
		}
	}
	return Profile{} // unreachable: defaults always contain "generic"
}

// Has reports whether key is a known profile.
func (r *Registry) Has(key string) bool {
	_, ok := r.byKey[key]
	return ok
}

// Add registers or overrides a profile.
func (r *Registry) Add(p Profile) {
	if _, ok := r.byKey[p.Key]; !ok {
		r.order = append(r.order, p.Key)
	}
	r.byKey[p.Key] = p
}

// Remove deletes a profile from the registry.
func (r *Registry) Remove(key string) {
	if _, ok := r.byKey[key]; !ok {
		return
	}
	delete(r.byKey, key)
	for i, k := range r.order {
		if k == key {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

// List returns profiles in registration order.
func (r *Registry) List() []Profile {
	out := make([]Profile, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, r.byKey[k])
	}
	return out
}
