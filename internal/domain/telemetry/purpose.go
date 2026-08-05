package telemetry

// Why an event is collected — and therefore what authorises collecting it.
//
// Splitting on PURPOSE rather than on a single on/off flag is the whole design.
// A first attempt gated everything behind one consent prompt, which meant a
// paying customer who declined opened their console and saw zero savings: we had
// broken the product they bought in order to protect them from data they had
// already paid to have collected.
//
// The distinction that actually matters is not what is collected but WHO IT IS
// FOR — and for exec_savings the answer depends on where it is used, which is
// why the boundary is drawn at cross-customer aggregation rather than at the
// client.
const (
	// PurposeService is data the customer bought: it is shown back to them in
	// their own console, and it is what the product reports about itself. A token
	// optimiser that cannot report its savings is missing half its product.
	//
	// Authorised by having an API key — an active key is a contract, and this
	// belongs in terms of service rather than in a consent prompt. Nothing is
	// sent at all without one: an unauthenticated sctx posts to loopback.
	PurposeService = "service"

	// PurposeImprovement is data WE learn from: which commands sctx fails to
	// cover, and therefore which ecosystems our customers actually work in.
	//
	// Opt-in, because its value comes from AGGREGATING it across customers, and
	// nobody agreed to that by buying a licence.
	PurposeImprovement = "improvement"
)

// PurposeOf reports why an event kind is collected.
//
// Deliberately a total function over kinds with an explicit default of
// PurposeImprovement: a kind nobody has classified is treated as the one needing
// consent. The opposite default would let a new event kind ship as "service" by
// omission, which is exactly how a payload quietly outgrows what customers agreed
// to. TestEveryEventKindHasAPurpose keeps the classification honest.
func PurposeOf(kind string) string {
	switch kind {
	case KindExecSavings:
		// The customer's own savings report, rendered on their own dashboards.
		// Aggregating it ACROSS customers is a separate question, answered by
		// consent — but that boundary is on the server, not here.
		return PurposeService
	case KindCoverageGap:
		// Purely ours: it ranks which formatter to build next. It tells the
		// customer nothing they do not already know from having run the command.
		return PurposeImprovement
	default:
		return PurposeImprovement
	}
}
