package main

var translations = map[string]map[string]string{
	"en": {
		"lang":              "en",
		"title":             "Circles",
		"lede":              "See who is closest to you on %s, from the favourites, replies and boosts on your latest public posts.",
		"login_button":      "Sign in with your account",
		"privacy":           "Each person only sees their own circle. Only your username is read, and the access token is revoked right after. People from other instances can appear in your circle.",
		"result":            "%s's circle",
		"download":          "Download image",
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
		"title":             "Círculos",
		"lede":              "Veja quem está mais perto de você na %s, pelos favoritos, respostas e impulsos nos seus últimos posts públicos.",
		"login_button":      "Entrar com a sua conta",
		"privacy":           "Cada pessoa só vê o próprio círculo. Só lemos o seu nome de usuário, e o token de acesso é revogado logo em seguida. Pessoas de outras instâncias podem aparecer no seu círculo.",
		"result":            "Círculo de %s",
		"download":          "Baixar imagem",
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

func strs(lang string) map[string]string {
	if t, ok := translations[lang]; ok {
		return t
	}
	return translations["en"]
}
