package eval

// Generation output ceilings for the eval pipeline, exported so the CLI cost
// estimators quote the SAME constants the spenders use: the confirm gate must
// never under-report what a run may bill (classic always-reasoning models
// bill reasoning against these budgets with no off switch). Budgets bound
// runaway cost; they must never fail a working model.
const (
	// JuryMaxTokens bounds one judge grading call (three axis scores; the
	// reasoning overhead is what needs the room).
	JuryMaxTokens = 1024
	// QAGenMaxTokens bounds one question-generation call (a one-line
	// question plus reasoning overhead).
	QAGenMaxTokens = 1024
)
