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
static CFMutableDictionaryRef makeDict(CFTypeRef *pairs, CFIndex n) {
	CFMutableDictionaryRef d = CFDictionaryCreateMutable(
		NULL, n, &kCFCopyStringDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	for (CFIndex i = 0; i < n; i += 2) {
		CFDictionaryAddValue(d, pairs[i], pairs[i + 1]);
	}
	return d;
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

// Коды ошибок Security.framework, которые обрабатываем вручную.
const (
	errSecSuccess       = 0
	errSecDuplicateItem = -25299 // запись уже есть — обновляем
	errSecItemNotFound  = -25300 // записи нет — не ошибка
	errSecAccessDenied  = -60008 // запись чужая: без промпта не прочитать
)

// ErrAccessDenied — запись создана другой сборкой бинаря: ACL Связки ключей
// привязан к подписи создателя, чужую подпись пускает только через промпт.
// Лечение — ввести токен заново в настройках: Save пересоздаст запись от
// текущей сборки.
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
	service := cfString(Service)
	defer C.CFRelease(C.CFTypeRef(service))
	acc := cfString(account)
	defer C.CFRelease(C.CFTypeRef(acc))
	data := cfData(secret)
	defer C.CFRelease(C.CFTypeRef(data))

	add := newDict(C.CFTypeRef(C.kSecClass), C.CFTypeRef(C.kSecClassGenericPassword),
		C.CFTypeRef(C.kSecAttrService), C.CFTypeRef(service),
		C.CFTypeRef(C.kSecAttrAccount), C.CFTypeRef(acc),
		C.CFTypeRef(C.kSecValueData), C.CFTypeRef(data))
	defer C.CFRelease(C.CFTypeRef(add))

	if st := C.SecItemAdd(C.CFDictionaryRef(add), nil); st != errSecSuccess {
		if st != errSecDuplicateItem {
			return osStatus("add", st)
		}
		// Запись уже есть. Пересоздаём её удалением и новым добавлением, а не
		// SecItemUpdate: запись могла остаться от прошлой сборки, и тогда новый
		// Add привязывает ACL к текущему бинарю — без лишних промптов дальше.
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
	service := cfString(Service)
	defer C.CFRelease(C.CFTypeRef(service))
	acc := cfString(account)
	defer C.CFRelease(C.CFTypeRef(acc))

	q := newDict(C.CFTypeRef(C.kSecClass), C.CFTypeRef(C.kSecClassGenericPassword),
		C.CFTypeRef(C.kSecAttrService), C.CFTypeRef(service),
		C.CFTypeRef(C.kSecAttrAccount), C.CFTypeRef(acc),
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
	service := cfString(Service)
	defer C.CFRelease(C.CFTypeRef(service))
	acc := cfString(account)
	defer C.CFRelease(C.CFTypeRef(acc))

	q := newDict(C.CFTypeRef(C.kSecClass), C.CFTypeRef(C.kSecClassGenericPassword),
		C.CFTypeRef(C.kSecAttrService), C.CFTypeRef(service),
		C.CFTypeRef(C.kSecAttrAccount), C.CFTypeRef(acc))
	defer C.CFRelease(C.CFTypeRef(q))

	if st := C.SecItemDelete(C.CFDictionaryRef(q)); st != errSecSuccess && st != errSecItemNotFound {
		return osStatus("delete", st)
	}
	return nil
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

func osStatus(op string, st C.OSStatus) error {
	if st == errSecAccessDenied {
		return fmt.Errorf("keychain %s: %w (osstatus %d)", op, ErrAccessDenied, int32(st))
	}
	return fmt.Errorf("keychain %s: osstatus %d", op, int32(st))
}
