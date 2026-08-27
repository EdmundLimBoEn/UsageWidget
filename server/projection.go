package server

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// paceProjection mirrors OpenUsage gauge pace: linear used% / elapsed window.
// Returns nil when the window label is unrecognized or pace cannot be computed.
func paceProjection(usedPercent float64, resetsAt time.Time, windowLabel string, now time.Time) *WindowForecast {
	windowDur, ok := gaugeWindowDuration(windowLabel)
	if !ok {
		return nil
	}
	resetIn := resetsAt.Sub(now)
	forecast := &WindowForecast{
		ComputedAt:  now.UTC(),
		Source:      "pace",
		WindowLabel: windowLabel,
	}

	var resetPart string
	if resetIn > 0 {
		resetPart = "resets " + formatDurationShort(resetIn)
	}

	var projPart string
	if usedPercent > 0 && usedPercent < 100 && resetIn < windowDur {
		elapsed := windowDur - resetIn
		if elapsed > 0 {
			elapsedMin := elapsed.Minutes()
			if elapsedMin > 0 {
				paceFraction := (usedPercent / 100) / elapsedMin
				if !math.IsNaN(paceFraction) && !math.IsInf(paceFraction, 0) && paceFraction > 0 {
					pctPerMinute := paceFraction * 100
					forecast.BurnRatePercentPerHour = pctPerMinute * 60
					forecast.BasedOnHours = elapsed.Hours()
					remainingPct := 100 - usedPercent
					minutesTo100 := remainingPct / pctPerMinute
					if minutesTo100 > 0 {
						d := time.Duration(minutesTo100 * float64(time.Minute))
						exhaustAt := now.Add(d).UTC()
						forecast.EstimatedExhaustionAt = exhaustAt
						forecast.ExhaustsBeforeReset = resetIn > 0 && d <= resetIn
						if resetIn > 0 && d > resetIn {
							projectedPct := usedPercent + pctPerMinute*resetIn.Minutes()
							n := int(math.Round(projectedPct))
							if n < 0 {
								n = 0
							}
							if n >= 100 {
								n = 99
							}
							pct := float64(n)
							forecast.ProjectedPercentAtReset = &pct
							projPart = fmt.Sprintf("~%d%% by reset", n)
						} else if d > 0 {
							projPart = "100% in " + formatDurationShort(d)
						}
					}
				}
			}
		}
	}

	forecast.Annotation = joinAnnotationParts(resetPart, projPart)
	if forecast.Annotation == "" && forecast.BurnRatePercentPerHour == 0 && forecast.EstimatedExhaustionAt.IsZero() {
		if resetPart != "" {
			forecast.Annotation = resetPart
			return forecast
		}
		return nil
	}
	if forecast.EstimatedExhaustionAt.IsZero() && resetIn > 0 {
		// Still useful to surface reset-only annotation when pace is unavailable.
		forecast.EstimatedExhaustionAt = resetsAt.UTC()
	}
	return forecast
}

func gaugeWindowDuration(window string) (time.Duration, bool) {
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "5h":
		return 5 * time.Hour, true
	case "1d", "24h", "today":
		return 24 * time.Hour, true
	case "7d":
		return 7 * 24 * time.Hour, true
	case "14d":
		return 14 * 24 * time.Hour, true
	case "30d":
		return 30 * 24 * time.Hour, true
	}
	return 0, false
}

func formatDurationShort(d time.Duration) string {
	if d <= 0 {
		return "0m"
	}
	if d >= 24*time.Hour {
		days := int(d / (24 * time.Hour))
		hours := int((d % (24 * time.Hour)) / time.Hour)
		if hours == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if d >= time.Hour {
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}

func joinAnnotationParts(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}
	return strings.Join(out, " · ")
}

// preferForecast picks pace (OpenUsage-style) when history regression is thin,
// otherwise keeps the richer history-based burn forecast and copies annotation.
func preferForecast(pace, history *WindowForecast) *WindowForecast {
	switch {
	case pace == nil:
		return history
	case history == nil:
		return pace
	case history.SampleCount >= 3 && history.BasedOnHours >= 0.5:
		// Keep history exhaustion semantics authoritative. Only borrow pace
		// annotation when both agree on exhausts-before-reset.
		if history.WindowLabel == "" {
			history.WindowLabel = pace.WindowLabel
		}
		if history.Source == "" {
			history.Source = "history"
		}
		if history.ExhaustsBeforeReset == pace.ExhaustsBeforeReset {
			if pace.Annotation != "" {
				history.Annotation = pace.Annotation
			}
			if pace.ProjectedPercentAtReset != nil {
				history.ProjectedPercentAtReset = pace.ProjectedPercentAtReset
			}
		} else if history.Annotation == "" {
			history.Annotation = paceAnnotationFromHistory(history)
		}
		return history
	default:
		return pace
	}
}

func paceAnnotationFromHistory(f *WindowForecast) string {
	if f == nil {
		return ""
	}
	if f.ExhaustsBeforeReset && !f.EstimatedExhaustionAt.IsZero() {
		base := f.ComputedAt
		if base.IsZero() {
			base = time.Now().UTC()
		}
		return "100% in " + formatDurationShort(f.EstimatedExhaustionAt.Sub(base))
	}
	if f.ProjectedPercentAtReset != nil {
		return fmt.Sprintf("~%.0f%% by reset", *f.ProjectedPercentAtReset)
	}
	return "on track until reset"
}
