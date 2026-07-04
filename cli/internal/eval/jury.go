package eval

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
)

// Judge is one juror: a name and a generation provider (ideally a model FROM A
// DIFFERENT FAMILY than the answer generator, to avoid self-preference bias).
type Judge struct {
	Name string
	Gen  ai.GenerationProvider
}

// Judgment is one juror's grade of one answer (1..5; 0 = unparseable/failed).
type Judgment struct {
	Judge string
	Score int
}

var scoreRe = regexp.MustCompile(`[1-5]`)

// ScoreAnswer asks each judge to grade the answer 1-5 for correctness and
// faithfulness to the ground-truth source note, returning the mean of the
// parseable scores and the per-judge breakdown.
func ScoreAnswer(ctx context.Context, judges []Judge, question, answer, sourceTitle, sourceBody string) (float64, []Judgment) {
	body := sourceBody
	if len([]rune(body)) > 3000 {
		body = string([]rune(body)[:3000])
	}
	prompt := fmt.Sprintf(`You are grading an answer produced by a retrieval-augmented QA system over a personal knowledge base.

QUESTION:
%s

GROUND-TRUTH SOURCE NOTE (titled %q) — the answer should be consistent with this note:
%s

ANSWER TO GRADE:
%s

Score the ANSWER from 1 to 5:
5 = fully correct, complete, and grounded in the source note
4 = correct and grounded, minor omission
3 = partially correct or partially grounded
2 = mostly wrong or largely ungrounded
1 = wrong, irrelevant, hallucinated, or a non-answer when the source clearly answers it

Respond with ONLY the single digit.`, question, sourceTitle, body, answer)

	var judgments []Judgment
	var sum, count float64
	for _, j := range judges {
		out, err := j.Gen.Generate(ctx, prompt, ai.GenOpts{MaxTokens: 8, Temperature: ai.Ptr(0.0)})
		score := 0
		if err == nil {
			score = parseScore(out)
		}
		judgments = append(judgments, Judgment{Judge: j.Name, Score: score})
		if score >= 1 {
			sum += float64(score)
			count++
		}
	}
	if count == 0 {
		return 0, judgments
	}
	return sum / count, judgments
}

// parseScore extracts the first 1-5 digit from a judge's reply.
func parseScore(s string) int {
	m := scoreRe.FindString(strings.TrimSpace(s))
	if m == "" {
		return 0
	}
	n, _ := strconv.Atoi(m)
	return n
}
