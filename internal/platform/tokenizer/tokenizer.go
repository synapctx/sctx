// Package tokenizer estimates token counts from byte lengths. It uses the
// deliberately conservative bytes/4 heuristic so reported savings stay
// honest.
package tokenizer

// EstimatorNote is the disclosure every surface that prints a token estimate
// (`sctx gain`, `sctx gain --share`, `sctx bench`) attaches next to the
// number, so nobody mistakes a floor for a measurement.
const EstimatorNote = "tokens = bytes/4, a floor"

// Estimate returns the approximate token count for n bytes of text.
func Estimate(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return (n + 3) / 4
}
