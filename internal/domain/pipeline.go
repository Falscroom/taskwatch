package domain

// PipelineStatus — статус пайплайна GitLab: success, failed, running, manual…
type PipelineStatus string

// Outcome — к чему сводится статус пайплайна для человека.
type Outcome int

const (
	OutcomeNone    Outcome = iota // пайплайна нет или он пропущен
	OutcomeRunning                // идёт, ждёт или неизвестен
	OutcomeSuccess
	OutcomeFailed
)

func (s PipelineStatus) Outcome() Outcome {
	switch s {
	case "success":
		return OutcomeSuccess
	case "failed", "canceled":
		return OutcomeFailed
	case "skipped", "":
		return OutcomeNone
	default:
		return OutcomeRunning
	}
}

type Pipeline struct {
	ID     int
	Status PipelineStatus
	WebURL string
}

// Outcome пайплайна; nil — пайплайна нет.
func (p *Pipeline) Outcome() Outcome {
	if p == nil {
		return OutcomeNone
	}
	return p.Status.Outcome()
}

// Failed — пайплайн упал или отменён.
func (p *Pipeline) Failed() bool { return p.Outcome() == OutcomeFailed }
