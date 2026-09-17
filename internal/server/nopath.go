package server

import "patmonitor/internal/rtc"

// noPathCause names the likeliest reason ICE found no path, and what to do.
//
// **It exists because the page cannot say it.** When the media does not pass the
// viewer reads `connection failed`, two words with no cause and nothing to do
// about them — and the published figures put that at roughly one connection in
// ten, which on a baby monitor is the person who most needs to see the room.
// The monitor, on the other hand, holds every fact that separates the causes,
// and it has a place to put them that survives the night: this is that place.
//
// **The order is the certainty scale**, as it is in trayStatus: each case is
// tested only once the ones above it have been excluded, so the earlier a cause
// sits the less it depends on the ones below being wrong.
//
// It runs in three passes, and the middle one is the correction a review caught.
// First the signalling, which is true of whoever is asking. Then **where they
// came in from**, because the questions underneath are about crossing the
// Internet and say nothing about a viewer who never had to: asked of a tailnet
// session, the network chain blames a guest Wi-Fi and then recommends the very
// tailnet the session was already on. Only then the road out.
//
// **And the last answer is "I do not know".** A plausible wrong cause sends
// whoever reads it to spend an evening on their router for a fault in their
// phone, and this line is read once, in the morning, by somebody who cannot
// re-run the failure: it is the same refusal guard.site makes about a stack
// frame it does not recognise. The facts go into the line either way.
func noPathCause(f rtc.ICEFacts, from origin) (cause, remedy string) {
	// The signalling, which is upstream of every road.
	switch {
	case f.Sent == 0:
		return "no-candidates-from-the-viewer",
			"the page answered and then sent no addresses of its own: the fault is " +
				"upstream of the network, in the browser or in the signalling channel"

	case f.Refused > 0 && f.Refused == f.Sent:
		return "candidates-refused",
			"the monitor refused every address the page sent, so this one is ours: " +
				"the discarded candidates are in the log at -v"
	}

	// Where they came in from. Everything below this point assumes the media had
	// to cross the Internet.
	switch from.Class {
	case originLocal, originThisPC:
		// Host candidates on both sides should be enough here, and STUN has
		// nothing to do with it.
		return "blocked-on-the-local-network",
			"both are on the same private network and nothing got through between " +
				"them: a guest Wi-Fi or client isolation on the access point stops " +
				"devices reaching each other, and so does a firewall on this machine"

	case originTailnet:
		// The viewer is already inside the private network, so there is no NAT
		// to punch and no public address to discover: what did not work is the
		// tailnet link itself, which has its own diagnosis and its own tools.
		return "blocked-on-the-tailnet",
			"the viewer is already inside the tailnet, so no NAT had to be crossed: " +
				"what did not carry is the tailnet link itself — `tailscale status` " +
				"on both ends, and `tailscale ping` between them, say whether it is up"

	case originUnknown:
		// The address would not parse, so which of the roads below applies is
		// exactly what is not known.
		return "unknown", "where the viewer came in from could not be established, " +
			"so none of the usual causes can be told apart"
	}

	// From the Internet, and in the order the evidence gets thinner.
	switch {
	case f.STUNUsable == 0:
		// It is deliberately not said that the file is empty: this count is what
		// survived validation, so every entry being refused looks identical from
		// here — and that refusal has its own line, earlier in the same log.
		//
		// **And an empty file is not merely indistinguishable from here: it
		// cannot happen.** `config.Load` restores the default pair whenever the
		// list comes out empty, `iceServers` is evaluated once on the value that
		// came straight out of `Load`, and no route touches that field — so the
		// only road to this branch is every entry being refused. The sentence
		// used to offer the impossible half first, three lines under a comment
		// refusing to say it, which sends whoever reads the log in the morning
		// to look at a file that is fine.
		return "no-stun-configured",
			"the monitor has no usable STUN server, so it cannot discover the " +
				"address it has on the Internet: every entry in stun_servers was " +
				"refused at start-up, which is said in this log when it happens"

	case f.LocalSrflx == 0:
		return "stun-did-not-answer",
			"the STUN servers never answered, so this machine did not learn its own " +
				"public address: outgoing UDP is blocked here, or those servers are " +
				"unreachable"

	case f.RemoteSrflx == 0:
		return "the-viewer-has-no-public-address",
			"the page offered only addresses of its own network: what it is " +
				"connected to blocks STUN, and until that changes the media has " +
				"nowhere to pass"

	case f.Knocked > 0 && f.Answered == 0:
		return "direct-path-refused",
			"both ends knew their public address and not one connectivity check was " +
				"answered: the network in between does not allow a direct path — a " +
				"mobile carrier's symmetric NAT, or UDP blocked. Install Tailscale on " +
				"the device you watch from and open the monitor over the tailnet"
	}

	return "unknown", "the facts below do not separate the known causes"
}
