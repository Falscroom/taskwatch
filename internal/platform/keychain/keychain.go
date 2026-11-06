// Package keychain — хранение секретов в macOS Keychain (Security.framework),
// чтобы в config.yaml на диске не лежало ничего confidential. Записи видны в
// приложении «Связка ключей» как сервис com.taskwatch.
package keychain

/*
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdlib.h>

// makeDict собирает CF-словарь из чередующихся ключей и значений (оба CFTypeRef).
// Словарь сам удерживает значения: их можно освободить сразу после вызова.
static CFMutableDictionaryRef makeDict(CFTypeRef *pairs, CFIndex n) {
	CFMutableDictionaryRef d = CFDictionaryCreateMutable(
		NULL, n, &kCFCopyStringDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	for (CFIndex i = 0; i < n; i += 2) {
		CFDictionaryAddValue(d, pairs[i], pairs[i + 1]);
	}
	return d;
}

// errorMessage — текст ошибки Security.framework по коду; NULL — текста нет.
// Освобождает вызывающий.
static char *errorMessage(OSStatus st) {
	CFStringRef s = SecCopyErrorMessageString(st, NULL);
	if (s == NULL) {
		return NULL;
	}
	CFIndex n = CFStringGetMaximumSizeForEncoding(CFStringGetLength(s), kCFStringEncodingUTF8) + 1;
	char *buf = malloc(n);
	if (!CFStringGetCString(s, buf, n, kCFStringEncodingUTF8)) {
		free(buf);
		buf = NULL;
	}
	CFRelease(s);
	return buf;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Service — имя сервиса в связке ключей.
const Service = "com.taskwatch"

// Коды Security.framework (SecBase.h, Authorization.h), которые разбираем сами.
const (
	errSecSuccess               = 0
	errSecUserCanceled          = -128   // в промпте доступа нажали «Запретить»
	errSecAuthFailed            = -25293 // доступ не дали
	errSecDuplicateItem         = -25299 // запись уже есть — пересоздаём
	errSecItemNotFound          = -25300 // записи нет — не ошибка
	errSecInteractionNotAllowed = -25308 // нужен промпт, а показать его нельзя
	errAuthorizationInternal    = -60008 // так отвечает чужая запись без промпта
)

// ErrAccessDenied — к записи не пустили. Обычно её создала другая сборка
// бинаря: доступ к записи привязан к подписи создателя, а чужую подпись macOS
// пускает только через промпт. Лечение — ввести токен заново в настройках:
// Save пересоздаст запись от текущей сборки.
var ErrAccessDenied = errors.New("запись создана другой сборкой приложения — введи токен заново в настройках")

// Store — Связка ключей как хранилище секретов: Get/Set/Delete поверх Load/Save/Delete.
type Store struct{}

func (Store) Get(account string) (string, error) { return Load(account) }
func (Store) Set(account, secret string) error   { return Save(account, secret) }
func (Store) Delete(account string) error        { return Delete(account) }

// Save создаёт запись (service, account) → secret, существующую обновляет.
// Запись принадлежит нашему бинарю: чужие приложения без разрешения прочитать
// её не могут.
func Save(account, secret string) error {
	data := cfData(secret)
	defer C.CFRelease(C.CFTypeRef(data))
	add := item(account, C.CFTypeRef(C.kSecValueData), C.CFTypeRef(data))
	defer C.CFRelease(C.CFTypeRef(add))

	if st := C.SecItemAdd(C.CFDictionaryRef(add), nil); st != errSecSuccess {
		if st != errSecDuplicateItem {
			return osStatus("add", st)
		}
		// Запись уже есть. Пересоздаём её удалением и новым добавлением, а не
		// SecItemUpdate: запись могла остаться от прошлой сборки, и тогда новый
		// Add привязывает доступ к текущему бинарю — без лишних промптов дальше.
		if err := Delete(account); err != nil {
			return err
		}
		if st := C.SecItemAdd(C.CFDictionaryRef(add), nil); st != errSecSuccess {
			return osStatus("add", st)
		}
	}
	return nil
}

// Load возвращает секрет; записи нет — пустая строка, это не ошибка.
func Load(account string) (string, error) {
	q := item(account,
		C.CFTypeRef(C.kSecReturnData), C.CFTypeRef(C.kCFBooleanTrue),
		C.CFTypeRef(C.kSecMatchLimit), C.CFTypeRef(C.kSecMatchLimitOne))
	defer C.CFRelease(C.CFTypeRef(q))

	var data C.CFTypeRef
	if st := C.SecItemCopyMatching(C.CFDictionaryRef(q), &data); st != errSecSuccess {
		if st == errSecItemNotFound {
			return "", nil
		}
		return "", osStatus("load", st)
	}
	defer C.CFRelease(data)

	n := int(C.CFDataGetLength(C.CFDataRef(data)))
	if n == 0 {
		return "", nil
	}
	return string(C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(C.CFDataRef(data))), C.int(n))), nil
}

// Delete удаляет запись; отсутствие записи ошибкой не считается.
func Delete(account string) error {
	q := item(account)
	defer C.CFRelease(C.CFTypeRef(q))

	if st := C.SecItemDelete(C.CFDictionaryRef(q)); st != errSecSuccess && st != errSecItemNotFound {
		return osStatus("delete", st)
	}
	return nil
}

// item — запрос к нашей записи account (пароль сервиса Service) с
// дополнительными парами ключ-значение; освобождать через CFRelease.
func item(account string, extra ...C.CFTypeRef) C.CFMutableDictionaryRef {
	service := cfString(Service)
	defer C.CFRelease(C.CFTypeRef(service))
	acc := cfString(account)
	defer C.CFRelease(C.CFTypeRef(acc))
	return newDict(append([]C.CFTypeRef{
		C.CFTypeRef(C.kSecClass), C.CFTypeRef(C.kSecClassGenericPassword),
		C.CFTypeRef(C.kSecAttrService), C.CFTypeRef(service),
		C.CFTypeRef(C.kSecAttrAccount), C.CFTypeRef(acc),
	}, extra...)...)
}

// newDict — CFMutableDictionaryRef из чередующихся ключей и значений;
// ключи — константы фреймворка, значения — тоже или наши CFString/CFData.
func newDict(pairs ...C.CFTypeRef) C.CFMutableDictionaryRef {
	return C.makeDict((*C.CFTypeRef)(unsafe.Pointer(&pairs[0])), C.CFIndex(len(pairs)))
}

// cfString — Go-строка в CFStringRef; освобождать через CFRelease.
func cfString(s string) C.CFStringRef {
	c := C.CString(s)
	defer C.free(unsafe.Pointer(c))
	return C.CFStringCreateWithCString(0, c, C.kCFStringEncodingUTF8)
}

// cfData — секрет в CFDataRef; CFDataCreate копирует байты, Go-строку можно освобождать.
func cfData(s string) C.CFDataRef {
	b := []byte(s)
	var p *C.UInt8
	if len(b) > 0 {
		p = (*C.UInt8)(unsafe.Pointer(&b[0]))
	}
	return C.CFDataCreate(0, p, C.CFIndex(len(b)))
}

func osStatus(op string, st C.OSStatus) error { return statusError(op, int32(st)) }

// statusError — ошибка с текстом от macOS и кодом. Отказ в доступе к записи — ErrAccessDenied.
func statusError(op string, code int32) error {
	msg := fmt.Sprintf("osstatus %d", code)
	if text := errorMessage(C.OSStatus(code)); text != "" {
		msg = fmt.Sprintf("%s (osstatus %d)", text, code)
	}
	switch code {
	case errSecUserCanceled, errSecAuthFailed, errSecInteractionNotAllowed, errAuthorizationInternal:
		return fmt.Errorf("keychain %s: %w: %s", op, ErrAccessDenied, msg)
	}
	return fmt.Errorf("keychain %s: %s", op, msg)
}

// errorMessage — текст ошибки Security.framework, пусто — если у кода текста нет.
func errorMessage(st C.OSStatus) string {
	c := C.errorMessage(st)
	if c == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(c))
	return C.GoString(c)
}
