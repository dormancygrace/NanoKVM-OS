package notify

import "fmt"

// Languages are the languages of the messages; the first is the default.
var Languages = []string{"en", "ru"}

type message struct{ title, text string }

// messages holds every message in every language; the text is a format for
// the event's Args.
var messages = map[string]map[string]message{
	"en": {
		"test":                 {"Test notification", "Notifications from this NanoKVM reach this channel."},
		"power.on":             {"Host powered on", "The host's power LED turned on."},
		"power.off":            {"Host powered off", "The host's power LED turned off."},
		"hdmi.lost":            {"HDMI signal lost", "The host stopped sending a picture."},
		"hdmi.restored":        {"HDMI signal restored", "The host sends a picture again."},
		"security.lockout":     {"Logins blocked", "%d failed logins from %s: logins from this address are blocked for %d min."},
		"security.new-address": {"Login from a new address", "%s logged in from %s, an address not seen before."},
		"screen.bsod":          {"Blue screen on the host", "Windows stopped with an error."},
		"screen.panic":         {"Kernel panic on the host", "The host's console shows a crash."},
	},
	"ru": {
		"test":                 {"Проверка уведомлений", "Уведомления от этого NanoKVM доходят до этого канала."},
		"power.on":             {"Хост включился", "Загорелся индикатор питания хоста."},
		"power.off":            {"Хост выключился", "Погас индикатор питания хоста."},
		"hdmi.lost":            {"Пропал HDMI-сигнал", "Хост перестал передавать изображение."},
		"hdmi.restored":        {"HDMI-сигнал вернулся", "Хост снова передаёт изображение."},
		"security.lockout":     {"Вход заблокирован", "%d неудачных входов с %s: вход с этого адреса заблокирован на %d мин."},
		"security.new-address": {"Вход с нового адреса", "Вход пользователя %s с %s: с этого адреса раньше не входили."},
		"screen.bsod":          {"Синий экран на хосте", "Windows остановилась с ошибкой."},
		"screen.panic":         {"Kernel panic на хосте", "На консоли хоста — сбой."},
	},
}

func render(e Event, language string) Rendered {
	catalogue, ok := messages[language]
	if !ok {
		catalogue = messages[Languages[0]]
	}
	m, ok := catalogue[e.Message]
	if !ok {
		m = message{e.Message, ""}
	}
	body := m.text
	if len(e.Args) > 0 {
		body = fmt.Sprintf(m.text, e.Args...)
	}
	if e.Details != "" {
		if body != "" {
			body += "\n\n"
		}
		body += e.Details
	}
	return Rendered{
		Kind:     e.Kind,
		Severity: e.Severity,
		Title:    m.title,
		Body:     body,
		Device:   deviceName(),
		Time:     e.Time,
		Image:    e.Image,
	}
}
