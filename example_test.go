package fingerprint_test

import (
	"fmt"

	"github.com/richardwooding/fingerprint"
)

// Example_textNearDuplicate shows detecting a near-duplicate document: a
// one-word edit leaves the SimHash fingerprints close (high Similarity),
// while unrelated prose sits near the random baseline.
func Example_textNearDuplicate() {
	base := "The cartographer unrolled the brittle chart across the captain's table and traced the reef-strewn passage with a steady finger, warning that the southern current would carry any vessel onto the rocks before dawn if the helmsman misjudged the tide by even a quarter hour."
	// One word changed (dawn → dusk) — a near-duplicate.
	edit := "The cartographer unrolled the brittle chart across the captain's table and traced the reef-strewn passage with a steady finger, warning that the southern current would carry any vessel onto the rocks before dusk if the helmsman misjudged the tide by even a quarter hour."
	// Entirely different prose.
	other := "Quarterly revenue rose twelve percent as the cloud division expanded its enterprise subscriptions, while the board approved a share buyback and raised guidance for the upcoming fiscal year."

	a, b, c := fingerprint.Compute(base), fingerprint.Compute(edit), fingerprint.Compute(other)
	fmt.Println(fingerprint.Similarity(a, b) > 0.85) // near-duplicate
	fmt.Println(fingerprint.Similarity(a, c) < 0.7)  // unrelated
	// Output:
	// true
	// true
}

// Example_imagePerceptualHash shows that PHashHex renders a 64-bit
// perceptual hash as a 16-character hex string for storage / comparison.
func Example_imagePerceptualHash() {
	// In practice: h, _ := fingerprint.PHash(reader)
	hex := fingerprint.PHashHex(0x0f0f0f0f0f0f0f0f)
	fmt.Println(len(hex), hex)
	// Output: 16 0f0f0f0f0f0f0f0f
}
