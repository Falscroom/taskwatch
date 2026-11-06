package tray

import (
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"taskwatch/internal/alerts"
)

func TestOptions(t *testing.T) {
	review := options(alerts.Event{
		Key: "review:7", Kind: alerts.KindReview, Title: "Нужно твоё ревью", Subtitle: "!7 · Fix", Body: "anna",
		URL: "https://gl/mr/7", Thread: "mr:7", MRID: 7,
	})
	if review.ID != "review:7" || review.Subtitle != "!7 · Fix" || review.ThreadID != "mr:7" ||
		review.CategoryID != categoryMR || review.Data["mr"] != "mr:7" || review.Data["url"] != "https://gl/mr/7" {
		t.Errorf("review = %+v", review)
	}
	if review.Sound != nil {
		t.Errorf("loud event must keep the default sound: %+v", review.Sound)
	}

	comment := options(alerts.Event{Key: "yt-comment:1", Kind: alerts.KindComment, Title: "Комментарий в таске", URL: "https://yt/1"})
	if comment.CategoryID != "" || comment.Sound == nil || !comment.Sound.Silent || comment.Data["mr"] != nil {
		t.Errorf("task comment = %+v", comment)
	}

	approved := options(alerts.Event{
		Key: "approved:7", Kind: alerts.KindApproved, MRID: 7,
		Transition: &alerts.Transition{IssueID: "APP-1", Command: "State Ready for QA"},
	})
	if approved.CategoryID != "transition:State Ready for QA" || approved.Data["issue"] != "APP-1" || approved.Data["mr"] != "mr:7" {
		t.Errorf("approved = %+v", approved)
	}
}

func TestRespond(t *testing.T) {
	var got []string
	do := responder{
		open:       func(url string) { got = append(got, "open "+url) },
		mute:       func(key string) { got = append(got, "mute "+key) },
		transition: func(id string) { got = append(got, "transition "+id) },
	}
	info := map[string]any{"url": "https://gl/mr/7", "mr": "mr:7", "issue": "APP-1"}
	for _, action := range []string{notifications.DefaultActionIdentifier, actionMute, actionTransition, "com.apple.UNNotificationDismissActionIdentifier"} {
		respond(notifications.NotificationResponse{ActionIdentifier: action, UserInfo: info}, do)
	}
	// Закрытие уведомления ничего не открывает.
	want := []string{"open https://gl/mr/7", "mute mr:7", "transition APP-1"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}

	// В примере уведомления данных нет — кнопки и клик ничего не делают.
	got = nil
	for _, action := range []string{notifications.DefaultActionIdentifier, actionMute, actionTransition} {
		respond(notifications.NotificationResponse{ActionIdentifier: action, UserInfo: map[string]any{"url": ""}}, do)
	}
	if len(got) != 0 {
		t.Errorf("empty data: got %v", got)
	}
}

// Вне бандла уведомления идут через запасной путь.
func TestNotifierFallsBackOutsideBundle(t *testing.T) {
	var sent []alerts.Event
	n := NewNotifier(func(ev alerts.Event) error { sent = append(sent, ev); return nil })
	if err := n.Notify(alerts.Event{Key: "k", Title: "T"}); err != nil || len(sent) != 1 || sent[0].Key != "k" {
		t.Errorf("sent = %v, err = %v", sent, err)
	}

	failing := NewNotifier(func(alerts.Event) error { return errors.New("no osascript") })
	if err := failing.Notify(alerts.Event{Key: "k", Title: "T"}); err == nil {
		t.Error("fallback error must reach the dispatcher, so the event is retried")
	}
}

// Сразу после запуска macOS отвечает «не разрешено» и только потом — как есть:
// событие откладывается, а когда разрешение приходит, опрос проходит заново.
func TestPermittedWaitsForPermission(t *testing.T) {
	defer func(w, p time.Duration) { permissionWait, permissionPoll = w, p }(permissionWait, permissionPoll)
	permissionWait, permissionPoll = time.Second, time.Millisecond

	var asks atomic.Int32
	refreshed := make(chan struct{}, 1)
	n := &Notifier{
		check:     func() (bool, error) { return asks.Add(1) > 3, nil },
		onAllowed: func() { refreshed <- struct{}{} },
	}
	if n.permitted() {
		t.Fatal("the first answer is «not allowed»")
	}
	select {
	case <-refreshed:
	case <-time.After(time.Second):
		t.Fatal("no refresh after the permission arrived")
	}
	if !n.permitted() {
		t.Error("permission arrived — must show now")
	}
}

// Разрешения так и не дали — одна строка в логе, без повторов.
func TestPermittedTurnedOff(t *testing.T) {
	defer func(w, p time.Duration) { permissionWait, permissionPoll = w, p }(permissionWait, permissionPoll)
	permissionWait, permissionPoll = 20*time.Millisecond, time.Millisecond
	var buf syncBuffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	n := &Notifier{check: func() (bool, error) { return false, nil }}
	for range 2 {
		if n.permitted() {
			t.Fatal("notifications are off")
		}
		for n.waiting.Load() {
			time.Sleep(time.Millisecond)
		}
	}
	if c := strings.Count(buf.String(), "уведомления taskwatch выключены"); c != 1 {
		t.Errorf("warnings = %d, want one:\n%s", c, buf.String())
	}
}

// syncBuffer — лог из фоновой горутины без гонок.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
