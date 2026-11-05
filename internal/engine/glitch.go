package engine

import (
	"log"

	"taskwatch/internal/domain"
)

// glitchGuard не верит внезапной пустоте. GitLab иногда отдаёт пустой список
// там, где только что были карточки, и ошибкой это не считается. Проглотить
// такой тик — значит на poll_interval остаться без своих MR. Пустоту принимаем
// только со второго подтверждения. Не потокобезопасен: живёт внутри Tick.
type glitchGuard struct {
	recheck bool // прошлый тик был подозрительным
}

// accept решает, верить ли снапшоту по сравнению с base. false — держать
// прошлый вид и перепроверить сразу: следующий тик подтвердит пустоту или
// опровергнет её.
func (g *glitchGuard) accept(base domain.Counts, snap *domain.Snapshot) bool {
	now := snap.Counts()
	emptied := (now.Own == 0 && base.Own > 0) ||
		(now.Reviews == 0 && base.Reviews > 0) ||
		(now.Tasks == 0 && base.Tasks > 0)
	switch {
	case emptied && !g.recheck:
		g.recheck = true
		log.Printf("подозрительный тик: свои MR %d→%d, ревью %d→%d, таски %d→%d — держу прошлый вид, перепроверяю сразу",
			base.Own, now.Own, base.Reviews, now.Reviews, base.Tasks, now.Tasks)
		return false
	case emptied:
		log.Print("пустота подтвердилась повторным тиком — считаю правдой")
	}
	g.recheck = false
	return true
}

// reset — тик не дал снапшота (ошибка, нет настроек): подозрение снимается.
func (g *glitchGuard) reset() { g.recheck = false }
