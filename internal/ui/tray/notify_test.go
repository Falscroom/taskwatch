package tray

import (
	"errors"
	"testing"

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
