package main

var translations = map[string]map[string]string{
	"en": {
		"lang":              "en",
		"title":             "Circles",
		"lede":              "See who is closest to you on %s, from the favourites, replies and boosts on your latest public posts.",
		"label":             "Your account",
		"button":            "Draw my circle",
		"privacy":           "Uses public interactions only. Only accounts of this instance can draw a circle, and the circle can include people from anywhere.",
		"result":            "%s's circle",
		"download":          "Download image",
		"another":           "Draw another",
		"inner":             "Inner circle",
		"middle":            "Middle circle",
		"outer":             "Outer circle",
		"empty":             "No interactions on the latest public posts yet.",
		"source":            "Source code",
		"credit":            "Based on Fedi Circle Creator",
		"error_instance":    "Only accounts of %s can draw a circle here.",
		"error_notfound":    "There is no such account on %s.",
		"error_handle":      "That doesn't look like a username.",
		"error_unavailable": "That didn't work. Please try again in a moment.",
	},
	"pt": {
		"lang":              "pt-BR",
		"title":             "Círculos",
		"lede":              "Veja quem está mais perto de você na %s, pelos favoritos, respostas e impulsos nos seus últimos posts públicos.",
		"label":             "Sua conta",
		"button":            "Desenhar meu círculo",
		"privacy":           "Usa apenas interações públicas. Só contas desta instância desenham um círculo, e nele podem aparecer pessoas de qualquer lugar.",
		"result":            "Círculo de %s",
		"download":          "Baixar imagem",
		"another":           "Desenhar outro",
		"inner":             "Círculo interno",
		"middle":            "Círculo do meio",
		"outer":             "Círculo externo",
		"empty":             "Ainda não há interações nos últimos posts públicos.",
		"source":            "Código-fonte",
		"credit":            "Baseado no Fedi Circle Creator",
		"error_instance":    "Só contas da %s podem desenhar um círculo aqui.",
		"error_notfound":    "Essa conta não existe na %s.",
		"error_handle":      "Isso não parece um nome de usuário.",
		"error_unavailable": "Não deu certo. Tente de novo em instantes.",
	},
}

func strs(lang string) map[string]string {
	if t, ok := translations[lang]; ok {
		return t
	}
	return translations["en"]
}
