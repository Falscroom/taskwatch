package domain

import "time"

// WorkHours — рабочее время: дни недели и часы, в которые taskwatch опрашивает
// сервисы. Вне него опрос спит. Часы — минуты от полуночи, Start < End.
type WorkHours struct {
	Days       [7]bool // по time.Weekday: Days[time.Monday] и так далее
	Start, End int     // минуты от полуночи
}

// Active — рабочее ли время в момент t.
func (w WorkHours) Active(t time.Time) bool {
	if !w.Days[t.Weekday()] {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	return m >= w.Start && m < w.End
}

// NextStart — ближайшее начало рабочего времени после t. Сегодняшнее, если оно
// ещё впереди, иначе — в ближайший рабочий день. Нулевое время — рабочих дней нет.
func (w WorkHours) NextStart(t time.Time) time.Time {
	for d := 0; d <= 7; d++ {
		day := t.AddDate(0, 0, d)
		if !w.Days[day.Weekday()] {
			continue
		}
		// time.Date, а не полночь + Start: в день перевода часов сутки не 24 часа.
		start := time.Date(day.Year(), day.Month(), day.Day(), w.Start/60, w.Start%60, 0, 0, t.Location())
		if start.After(t) {
			return start
		}
	}
	return time.Time{}
}
