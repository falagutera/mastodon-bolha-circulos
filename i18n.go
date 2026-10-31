package main

import (
	"net/http"
	"strings"
)

const langCookie = "__Host-circles-lang"

var translations = map[string]map[string]string{
	"en": {
		"lang":              "en",
		"language":          "Language",
		"title":             "Circles",
		"card_eyebrow":      "Fediverse circle",
		"card_closest":      "Closest to you",
		"card_more":         "+ %d people in the outer rings",
		"lede":              "See who is closest to you on %s, from the favourites, replies and boosts on your latest public posts.",
		"login_button":      "Sign in with your account",
		"privacy":           "Each person only sees their own circle. Only your username is read, and the access token is revoked right after. Posting asks for your approval on the instance, publishes one public post, and that token is revoked too. People from other instances can appear in your circle.",
		"result":            "%s's circle",
		"download":          "Download image",
		"share":             "Post on Mastodon",
		"share_text":        "My fediverse circle. Draw yours at %s",
		"logout":            "Sign out",
		"inner":             "Inner circle",
		"middle":            "Middle circle",
		"outer":             "Outer circle",
		"empty":             "No interactions on your latest public posts yet.",
		"source":            "Source code",
		"credit":            "Based on Fedi Circle Creator",
		"error_expired":     "The sign-in expired. Please try again.",
		"error_denied":      "Sign-in was cancelled on the instance.",
		"error_instance":    "The instance couldn't be reached. Please try again in a moment.",
		"error_account":     "This account can't draw a circle.",
		"error_unavailable": "That didn't work. Please try again in a moment.",
	},
	"pt": {
		"lang":              "pt-BR",
		"language":          "Idioma",
		"title":             "Círculos",
		"card_eyebrow":      "Círculo no fediverso",
		"card_closest":      "Mais perto de você",
		"card_more":         "+ %d pessoas nos anéis de fora",
		"lede":              "Veja quem está mais perto de você na %s, pelos favoritos, respostas e impulsos nos seus últimos posts públicos.",
		"login_button":      "Entrar com a sua conta",
		"privacy":           "Cada pessoa só vê o próprio círculo. Só lemos o seu nome de usuário, e o token de acesso é revogado logo em seguida. Postar pede a sua aprovação na instância, publica um post público, e esse token também é revogado. Pessoas de outras instâncias podem aparecer no seu círculo.",
		"result":            "Círculo de %s",
		"download":          "Baixar imagem",
		"share":             "Postar no Mastodon",
		"share_text":        "Meu círculo no fediverso. Faça o seu em %s",
		"logout":            "Sair",
		"inner":             "Círculo interno",
		"middle":            "Círculo do meio",
		"outer":             "Círculo externo",
		"empty":             "Ainda não há interações nos seus últimos posts públicos.",
		"source":            "Código-fonte",
		"credit":            "Baseado no Fedi Circle Creator",
		"error_expired":     "O login expirou. Tente de novo.",
		"error_denied":      "O login foi cancelado na instância.",
		"error_instance":    "Não consegui falar com a instância. Tente de novo em instantes.",
		"error_account":     "Esta conta não pode desenhar um círculo.",
		"error_unavailable": "Não deu certo. Tente de novo em instantes.",
	},
}

// language picks the page language: the visitor's choice, then their browser,
// then the instance default.
func language(r *http.Request, fallback string) string {
	if c, err := r.Cookie(langCookie); err == nil {
		if _, ok := translations[c.Value]; ok {
			return c.Value
		}
	}
	for part := range strings.SplitSeq(r.Header.Get("Accept-Language"), ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		base, _, _ := strings.Cut(strings.ToLower(tag), "-")
		if _, ok := translations[base]; ok {
			return base
		}
	}
	return fallback
}

func newCardText(lang string) cardText {
	t := strs(lang)
	return cardText{Eyebrow: t["card_eyebrow"], Closest: t["card_closest"], More: t["card_more"]}
}

func strs(lang string) map[string]string {
	if t, ok := translations[lang]; ok {
		return t
	}
	return translations["en"]
}
