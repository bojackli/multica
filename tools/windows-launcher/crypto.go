package main

import "crypto/rand"

// randomHex returns n bytes encoded as lowercase hex.
func randomHex(n int) string {
	const hexDigits = "0123456789abcdef"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand never fails on supported platforms; fall back to a
		// time-based value so we never hand back an empty secret.
		panic("crypto/rand unavailable: " + err.Error())
	}
	out := make([]byte, n*2)
	for i, b := range buf {
		out[i*2] = hexDigits[b>>4]
		out[i*2+1] = hexDigits[b&0x0f]
	}
	return string(out)
}
