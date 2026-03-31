package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// tunnelDomain computes a deterministic, human-readable domain prefix from the
// working directory and port number.  The result has the form "t-adjective-noun"
// and is stable for a given (cwd, port) pair.
func tunnelDomain(cwd string, port int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", cwd, port)))
	adjIdx := binary.BigEndian.Uint16(h[0:2]) % uint16(len(adjectives))
	nounIdx := binary.BigEndian.Uint16(h[2:4]) % uint16(len(nouns))
	return fmt.Sprintf("t-%s-%s", adjectives[adjIdx], nouns[nounIdx])
}

// 128 adjectives – short, easy to read, DNS-safe (lowercase, no hyphens).
var adjectives = []string{
	"able", "bold", "calm", "dark", "easy", "fair", "glad", "hale",
	"idle", "just", "keen", "live", "mild", "neat", "open", "pale",
	"pure", "rare", "safe", "tall", "used", "vast", "warm", "zany",
	"aged", "blue", "cold", "deep", "even", "fast", "gold", "hard",
	"icy", "kind", "lean", "main", "new", "odd", "pink", "rich",
	"slim", "thin", "ugly", "weak", "wise", "apt", "big", "coy",
	"dim", "dry", "dull", "fat", "few", "fit", "flat", "free",
	"full", "gray", "grim", "half", "high", "holy", "huge", "lame",
	"last", "late", "lazy", "left", "long", "lost", "loud", "low",
	"mad", "next", "nice", "only", "oval", "plus", "poor", "raw",
	"real", "red", "ripe", "rude", "shut", "sick", "soft", "sore",
	"sour", "tame", "tiny", "torn", "trim", "true", "twin", "upon",
	"very", "vile", "wary", "wet", "wide", "wild", "worn", "wry",
	"avid", "bare", "busy", "cool", "cozy", "damp", "dear", "deft",
	"epic", "fine", "firm", "fond", "foul", "glib", "good", "gory",
	"gray", "grim", "hazy", "holm", "iron", "jade", "lacy", "lush",
}

// 128 nouns – short, common, DNS-safe.
var nouns = []string{
	"ant", "bat", "cat", "dog", "elk", "fox", "gem", "hat",
	"ice", "jay", "key", "log", "map", "net", "oak", "pen",
	"ram", "sun", "tin", "urn", "van", "web", "yak", "zoo",
	"ace", "bag", "box", "bus", "cap", "cup", "den", "dot",
	"ear", "egg", "eye", "fan", "fig", "fin", "fly", "fog",
	"gap", "gum", "hen", "hog", "hub", "ink", "ion", "ivy",
	"jam", "jar", "jet", "jig", "jot", "kit", "lab", "lap",
	"law", "leg", "lid", "lip", "lot", "mix", "mob", "mud",
	"mug", "nap", "nut", "oar", "orb", "ore", "owl", "pad",
	"pan", "paw", "pea", "pie", "pig", "pin", "pit", "pod",
	"pot", "pub", "pup", "rag", "rat", "ray", "rib", "rod",
	"row", "rug", "rut", "saw", "sky", "spy", "tab", "tag",
	"tap", "tax", "toe", "top", "toy", "tub", "tug", "vet",
	"vine", "wax", "wig", "wit", "wolf", "worm", "yam", "yarn",
	"bell", "bird", "boat", "bone", "book", "bulb", "cake", "claw",
	"coin", "crab", "crow", "dawn", "deer", "dove", "drum", "dusk",
}
