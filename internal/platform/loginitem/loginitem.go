// Package loginitem — запуск taskwatch при входе в систему через «Объекты входа»
// macOS (SMAppService, macOS 13+). Работает только из бандла taskwatch.app:
// голый бинарь macOS зарегистрировать не даст.
package loginitem

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework ServiceManagement
#include <stdlib.h>
#import <Foundation/Foundation.h>
#import <ServiceManagement/ServiceManagement.h>

static int loginItemInBundle(void) {
	return [NSBundle mainBundle].bundleIdentifier != nil;
}

static int loginItemStatus(void) {
	return (int)[SMAppService mainAppService].status;
}

// loginItemSet регистрирует (on=1) или снимает (on=0) объект входа; NULL — успех,
// иначе текст ошибки, который освобождает вызывающий.
static char *loginItemSet(int on) {
	NSError *err = nil;
	BOOL ok = on ? [[SMAppService mainAppService] registerAndReturnError:&err]
	             : [[SMAppService mainAppService] unregisterAndReturnError:&err];
	if (ok) {
		return NULL;
	}
	return strdup(err ? err.localizedDescription.UTF8String : "unknown error");
}
*/
import "C"

import (
	"errors"
	"unsafe"
)

// Status — состояние объекта входа (SMAppServiceStatus).
type Status int

const (
	NotRegistered    Status = 0 // не добавлен
	Enabled          Status = 1 // добавлен и включён
	RequiresApproval Status = 2 // добавлен, но выключен в Системных настройках
	NotFound         Status = 3 // не найден: так macOS отвечает и до первой регистрации
)

// Current — текущее состояние. До первой регистрации macOS отдаёт NotFound —
// по нему не понять, бандл это или голый бинарь; для этого есть InBundle.
func Current() Status { return Status(C.loginItemStatus()) }

// InBundle — запущено ли приложение из бандла с идентификатором: только таким
// можно управлять как объектом входа.
func InBundle() bool { return C.loginItemInBundle() != 0 }

// Registered — добавлен ли объект входа: включённым или выключенным в
// Системных настройках.
func Registered() bool {
	s := Current()
	return s == Enabled || s == RequiresApproval
}

func (s Status) String() string {
	switch s {
	case NotRegistered, NotFound:
		return "не добавлен"
	case Enabled:
		return "включён"
	case RequiresApproval:
		return "выключен в Системных настройках → Основные → Объекты входа"
	default:
		return "неизвестно"
	}
}

// Set добавляет приложение в объекты входа или убирает его оттуда.
func Set(on bool) error {
	flag := C.int(0)
	if on {
		flag = 1
	}
	if msg := C.loginItemSet(flag); msg != nil {
		defer C.free(unsafe.Pointer(msg))
		return errors.New(C.GoString(msg))
	}
	return nil
}

// Item — объект входа для окна настроек: состояние и переключатель.
type Item struct{}

// Supported — можно ли управлять объектом входа: только из бандла.
func (Item) Supported() bool { return InBundle() }

// Enabled — включён ли запуск при входе.
func (Item) Enabled() bool { return Current() == Enabled }

// SetEnabled включает или выключает запуск при входе.
func (Item) SetEnabled(on bool) error { return Set(on) }
