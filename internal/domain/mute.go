package domain

import (
	"strconv"
	"strings"
	"time"
)

// Ключи заглушений. Они же лежат в state.json — менять формат нельзя.
const (
	mrKeyPrefix    = "mr:"
	titleKeyPrefix = "title:"
)

// MRKey — ключ MR: "mr:<id>". Им же помечена карточка MR в окне.
func MRKey(id int) string { return mrKeyPrefix + strconv.Itoa(id) }

// IsMRKey — ключ указывает на MR.
func IsMRKey(key string) bool { return strings.HasPrefix(key, mrKeyPrefix) }

// TitleKey — ключ правила «все MR с таким названием», включая будущие.
func TitleKey(title string) string { return titleKeyPrefix + title }

// MuteRules — заглушения: ключ → до когда; нулевое время — навсегда.
type MuteRules map[string]time.Time

// MuteActive — действует ли заглушение until на момент now.
func MuteActive(until, now time.Time) bool { return until.IsZero() || until.After(now) }

// Match — заглушён ли MR: личным правилом или правилом по названию. Возвращает,
// до когда; нулевое время — навсегда.
func (r MuteRules) Match(mr *MR, now time.Time) (until time.Time, muted bool) {
	for _, key := range []string{MRKey(mr.ID), TitleKey(mr.Title)} {
		if until, ok := r[key]; ok && MuteActive(until, now) {
			return until, true
		}
	}
	return time.Time{}, false
}
