package fingerprint_test

import (
	"fmt"
	"os"

	"github.com/richardwooding/fingerprint"
)

// The 1024 bytes an ISO 24138 Image-Code is computed from. Pair this with
// github.com/iscc/iscc-lib/packages/go, which takes exactly these bytes:
//
//	code, err := iscc.GenImageCodeV0(pixels, 64)  // ISCC:EEA4GQZQTY6J5DTH
//
// This package computes no code itself, and takes no dependency on one.
func Example_isccNormalisation() {
	f, err := os.Open("testdata/iscc_demo.png")
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()

	pixels, err := fingerprint.ISCCPixelsFromReader(f)
	if err != nil {
		panic(err)
	}
	fmt.Println("pixels:", len(pixels))
	// %d, not Println: these are grayscale values, not text.
	fmt.Printf("first four: %d\n", pixels[:4])
	// Output:
	// pixels: 1024
	// first four: [25 18 14 15]
}
