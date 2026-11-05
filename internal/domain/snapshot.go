package domain

// Snapshot — всё, что собрано за один проход опроса.
type Snapshot struct {
	Tasks []*Task
	// Orphans — свои MR без таски из спринта.
	Orphans []*MR
	// Reviews — MR других людей, где ты ревьюер.
	Reviews []*MR
}

// AllMRs — все MR снапшота одним списком: для обходов, которым не важно происхождение.
func (s *Snapshot) AllMRs() []*MR {
	out := make([]*MR, 0, len(s.Orphans)+len(s.Reviews))
	for _, t := range s.Tasks {
		out = append(out, t.MRs...)
	}
	out = append(out, s.Orphans...)
	return append(out, s.Reviews...)
}

// Counts — размеры снапшота по разделам.
type Counts struct {
	Own     int // свои MR: привязанные к таскам + сироты
	Reviews int // MR, где ты ревьюер
	Tasks   int // таски YouTrack
}

func (s *Snapshot) Counts() Counts {
	c := Counts{Own: len(s.Orphans), Reviews: len(s.Reviews), Tasks: len(s.Tasks)}
	for _, t := range s.Tasks {
		c.Own += len(t.MRs)
	}
	return c
}
