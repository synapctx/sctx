// Package tokenizer estimates token counts from byte lengths. It uses the
// deliberately conservative bytes/4 heuristic so reported savings stay
// honest.
package tokenizer

// Estimate returns the approximate token count for n bytes of text.
func Estimate(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return (n + 3) / 4
}
