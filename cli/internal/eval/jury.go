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

// Judgment is one juror's multi-axis grade of one answer (each 1..5; a value of
// 0 means that judge's reply was unparseable and is excluded from the means).
type Judgment struct {
	Judge        string
	Correctness  int
	Completeness int
	Grounding    int
}

// ok reports whether all three axes parsed into the 1..5 range.
func (j Judgment) ok() bool {
	return j.Correctness >= 1 && j.Completeness >= 1 && j.Grounding >= 1
}

// AnswerScore is the jury's aggregate grade of one answer: per-axis means across
// the judges that returned a parseable verdict, plus a composite (mean of the
// three axes). Separating the axes is the point — a "be thorough" prompt should
// lift Completeness, but the risk is a Grounding (hallucination) regression, and
// a blended single score would hide exactly that tradeoff.
type AnswerScore struct {
	Composite    float64
	Correctness  float64
	Completeness float64
	Grounding    float64
	NJudges      int
	Judgments    []Judgment
}

var axisRes = map[string]*regexp.Regexp{
	"correctness":  regexp.MustCompile(`(?i)correctness\s*[:=]\s*([1-5])`),
	"completeness": regexp.MustCompile(`(?i)completeness\s*[:=]\s*([1-5])`),
	"grounding":    regexp.MustCompile(`(?i)grounding\s*[:=]\s*([1-5])`),
}

// ScoreAnswer asks each judge to grade the answer on correctness, completeness,
// and grounding (each 1-5) against the ground-truth source note, and returns the
// per-axis means + composite across the judges that returned a parseable verdict.
func ScoreAnswer(ctx context.Context, judges []Judge, question, answer, sourceTitle, sourceBody string) AnswerScore {
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

Grade the ANSWER on three axes, each an integer 1-5:
- CORRECTNESS: is what it states accurate per the source note? (5 = fully correct, 1 = wrong)
- COMPLETENESS: does it cover the relevant details the source provides for this question? (5 = complete, 1 = misses most)
- GROUNDING: is every claim supported by the source, with no invented or hallucinated detail? (5 = fully grounded, 1 = largely fabricated)

Respond with EXACTLY three lines and nothing else:
CORRECTNESS: <1-5>
COMPLETENESS: <1-5>
GROUNDING: <1-5>`, question, sourceTitle, body, answer)

	var sc AnswerScore
	var sumC, sumP, sumG float64
	for _, j := range judges {
		out, err := j.Gen.Generate(ctx, prompt, ai.GenOpts{MaxTokens: 40, Temperature: ai.Ptr(0.0)})
		jd := Judgment{Judge: j.Name}
		if err == nil {
			jd.Correctness, jd.Completeness, jd.Grounding = parseAxes(out)
		}
		sc.Judgments = append(sc.Judgments, jd)
		if jd.ok() {
			sumC += float64(jd.Correctness)
			sumP += float64(jd.Completeness)
			sumG += float64(jd.Grounding)
			sc.NJudges++
		}
	}
	if sc.NJudges == 0 {
		return sc
	}
	n := float64(sc.NJudges)
	sc.Correctness = sumC / n
	sc.Completeness = sumP / n
	sc.Grounding = sumG / n
	sc.Composite = (sc.Correctness + sc.Completeness + sc.Grounding) / 3
	return sc
}

// parseAxes extracts the three axis scores from a judge reply. A missing axis
// stays 0, which makes Judgment.ok() false so the whole verdict is skipped
// (a partial grade would bias the means).
func parseAxes(s string) (correctness, completeness, grounding int) {
	return axisScore(s, "correctness"), axisScore(s, "completeness"), axisScore(s, "grounding")
}

func axisScore(s, axis string) int {
	m := axisRes[axis].FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// scoreRe matches a standalone 1-5 digit (not part of a larger number like "10"
// or a year). Retained for any single-axis parsing/tests.
var scoreRe = regexp.MustCompile(`(?:^|[^0-9])([1-5])(?:[^0-9]|$)`)

func parseScore(s string) int {
	m := scoreRe.FindStringSubmatch(strings.TrimSpace(s))
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}
