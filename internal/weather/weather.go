// Package weather: погода и геокодинг через Open-Meteo (бесплатно, без API-ключа).
package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrNotFound = errors.New("город не найден")

type Client struct{ http *http.Client }

func New() *Client {
	return &Client{http: &http.Client{Timeout: 15 * time.Second}}
}

// Place — результат геокодинга (Open-Meteo сразу отдаёт часовой пояс).
type Place struct {
	Name      string  `json:"name"`
	Country   string  `json:"country"`
	Admin1    string  `json:"admin1"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Timezone  string  `json:"timezone"`
}

// DisplayName — человекочитаемое название: "Казань", "Springfield, Illinois, США".
func (p Place) DisplayName() string {
	parts := []string{p.Name}
	if p.Admin1 != "" && !strings.EqualFold(p.Admin1, p.Name) {
		parts = append(parts, p.Admin1)
	}
	if p.Country != "" {
		parts = append(parts, p.Country)
	}
	return strings.Join(parts, ", ")
}

func (c *Client) Geocode(ctx context.Context, query string) (*Place, error) {
	q := url.Values{}
	q.Set("name", query)
	q.Set("count", "1")
	q.Set("language", "ru")
	q.Set("format", "json")

	place, err := c.getJSON(ctx, "https://geocoding-api.open-meteo.com/v1/search?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if len(place.Results) == 0 {
		return nil, ErrNotFound
	}
	return &place.Results[0], nil
}

type geocodeResp struct {
	Results []Place `json:"results"`
}

func (c *Client) getJSON(ctx context.Context, rawURL string) (*geocodeResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("запрос к Open-Meteo: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Open-Meteo: статус %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	var parsed geocodeResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("ответ Open-Meteo не разобрать: %w", err)
	}
	return &parsed, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Daily — прогноз на сегодня.
type Daily struct {
	City       string
	DateHuman  string // "25 ноября"
	Code       int    // WMO weather code
	TempMin    float64
	TempMax    float64
	Morning    float64 // температура утром (~08:00)
	Day        float64 // днём (~13:00)
	Evening    float64 // вечером (~19:00)
	PrecipMM   float64
	PrecipProb int // максимальная вероятность осадков, %
	WindMS     float64
	Sunrise    string // "08:12"
	Sunset     string // "16:25"
}

func (c *Client) DailyForecast(ctx context.Context, lat, lon float64, timezone string) (*Daily, error) {
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(lat, 'f', 4, 64))
	q.Set("longitude", strconv.FormatFloat(lon, 'f', 4, 64))
	q.Set("timezone", timezone)
	q.Set("forecast_days", "1")
	q.Set("wind_speed_unit", "ms")
	q.Set("daily", "temperature_2m_max,temperature_2m_min,precipitation_sum,precipitation_probability_max,weathercode,sunrise,sunset,wind_speed_10m_max")
	q.Set("hourly", "temperature_2m,precipitation_probability")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.open-meteo.com/v1/forecast?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("запрос к Open-Meteo: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Open-Meteo: статус %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var api struct {
		Daily struct {
			Time          []string  `json:"time"`
			TempMax       []float64 `json:"temperature_2m_max"`
			TempMin       []float64 `json:"temperature_2m_min"`
			PrecipSum     []float64 `json:"precipitation_sum"`
			PrecipProbMax []int     `json:"precipitation_probability_max"`
			Code          []int     `json:"weathercode"`
			Sunrise       []string  `json:"sunrise"`
			Sunset        []string  `json:"sunset"`
			WindMax       []float64 `json:"wind_speed_10m_max"`
		} `json:"daily"`
		Hourly struct {
			Time       []string  `json:"time"`
			Temp       []float64 `json:"temperature_2m"`
			PrecipProb []int     `json:"precipitation_probability"`
		} `json:"hourly"`
	}
	if err := json.Unmarshal(body, &api); err != nil {
		return nil, fmt.Errorf("ответ Open-Meteo не разобрать: %w", err)
	}
	if len(api.Daily.Time) == 0 || len(api.Daily.TempMax) == 0 {
		return nil, fmt.Errorf("Open-Meteo вернул пустой прогноз")
	}

	d := &Daily{
		TempMin:    api.Daily.TempMin[0],
		TempMax:    api.Daily.TempMax[0],
		Code:       firstInt(api.Daily.Code),
		PrecipMM:   firstFloat(api.Daily.PrecipSum),
		PrecipProb: firstInt(api.Daily.PrecipProbMax),
		WindMS:     firstFloat(api.Daily.WindMax),
		Sunrise:    hhmm(firstString(api.Daily.Sunrise)),
		Sunset:     hhmm(firstString(api.Daily.Sunset)),
	}
	if t, err := time.Parse("2006-01-02", api.Daily.Time[0]); err == nil {
		d.DateHuman = fmt.Sprintf("%d %s", t.Day(), monthName(t.Month()))
	} else {
		d.DateHuman = api.Daily.Time[0]
	}

	d.Morning = hourlyValue(api.Hourly.Time, api.Hourly.Temp, 8, d.TempMin)
	d.Day = hourlyValue(api.Hourly.Time, api.Hourly.Temp, 13, d.TempMax)
	d.Evening = hourlyValue(api.Hourly.Time, api.Hourly.Temp, 19, d.TempMin)

	return d, nil
}

func hourlyValue(times []string, values []float64, hour int, fallback float64) float64 {
	suffix := fmt.Sprintf("T%02d:00", hour)
	for i, t := range times {
		if strings.HasSuffix(t, suffix) && i < len(values) {
			return values[i]
		}
	}
	return fallback
}

func firstString(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

func firstInt(s []int) int {
	if len(s) > 0 {
		return s[0]
	}
	return 0
}

func firstFloat(s []float64) float64 {
	if len(s) > 0 {
		return s[0]
	}
	return 0
}

// hhmm из "2026-11-25T08:12" делает "08:12".
func hhmm(iso string) string {
	if len(iso) >= 16 && iso[10] == 'T' {
		return iso[11:16]
	}
	return iso
}

var months = [...]string{
	"января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря",
}

func monthName(m time.Month) string {
	if int(m) >= 1 && int(m) <= 12 {
		return months[m-1]
	}
	return ""
}
