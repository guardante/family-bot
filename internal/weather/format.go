package weather

import (
	"fmt"
	"strings"
)

// DescribeCode превращает WMO weather code в эмодзи и описание на русском.
func DescribeCode(code int) (emoji, text string) {
	switch code {
	case 0:
		return "☀️", "ясно"
	case 1:
		return "🌤", "преимущественно ясно"
	case 2:
		return "⛅️", "переменная облачность"
	case 3:
		return "☁️", "пасмурно"
	case 45, 48:
		return "🌫", "туман"
	case 51:
		return "🌦", "слабая морось"
	case 53:
		return "🌦", "морось"
	case 55:
		return "🌧", "сильная морось"
	case 56, 57:
		return "🌧", "ледяная морось"
	case 61:
		return "🌦", "небольшой дождь"
	case 63:
		return "🌧", "дождь"
	case 65:
		return "🌧", "сильный дождь"
	case 66, 67:
		return "🌧", "ледяной дождь"
	case 71:
		return "🌨", "небольшой снег"
	case 73:
		return "❄️", "снег"
	case 75:
		return "❄️", "сильный снег"
	case 77:
		return "🌨", "снежные зёрна"
	case 80:
		return "🌦", "кратковременные дожди"
	case 81:
		return "🌧", "ливень"
	case 82:
		return "⛈", "сильный ливень"
	case 85:
		return "🌨", "снегопад"
	case 86:
		return "❄️", "сильный снегопад"
	case 95:
		return "⛈", "гроза"
	case 96, 99:
		return "⛈", "гроза с градом"
	default:
		return "🌡", "неопределённо"
	}
}

func temp(t float64) string {
	return fmt.Sprintf("%+.0f°", t)
}

// FormatDaily собирает текстовое сообщение с погодой на день.
func FormatDaily(d *Daily) string {
	emoji, desc := DescribeCode(d.Code)

	var b strings.Builder
	cityPart := ""
	if d.City != "" {
		cityPart = " в " + d.City
	}
	fmt.Fprintf(&b, "%s Погода%s на %s — %s\n\n", emoji, cityPart, d.DateHuman, desc)

	fmt.Fprintf(&b, "🌡 Утром %s, днём %s, вечером %s\n", temp(d.Morning), temp(d.Day), temp(d.Evening))
	fmt.Fprintf(&b, "📈 Минимум %s, максимум %s\n", temp(d.TempMin), temp(d.TempMax))

	if d.PrecipMM > 0.05 {
		fmt.Fprintf(&b, "🌧 Осадки: %.1f мм (вероятность %d%%)\n", d.PrecipMM, d.PrecipProb)
	} else if d.PrecipProb >= 30 {
		fmt.Fprintf(&b, "🌧 Возможны осадки, вероятность %d%%\n", d.PrecipProb)
	} else {
		b.WriteString("🌤 Без осадков\n")
	}

	fmt.Fprintf(&b, "💨 Ветер до %.0f м/с\n", d.WindMS)
	fmt.Fprintf(&b, "🌅 Восход %s • 🌇 Закат %s", d.Sunrise, d.Sunset)

	return b.String()
}
