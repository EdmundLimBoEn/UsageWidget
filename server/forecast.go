package server

import (
	"database/sql"
	"fmt"
	"math"
	"time"
)

type WindowForecast struct {
	ComputedAt             time.Time `json:"computedAt"`
	BurnRatePercentPerHour float64   `json:"burnRatePercentPerHour"`
	EstimatedExhaustionAt  time.Time `json:"estimatedExhaustionAt"`
	ExhaustsBeforeReset    bool      `json:"exhaustsBeforeReset"`
	SampleCount            int       `json:"sampleCount"`
	BasedOnHours           float64   `json:"basedOnHours"`
}

type usageSample struct {
	at   time.Time
	used float64
}

func forecastWindowTx(tx *sql.Tx, windowID, epoch string, resetsAt time.Time, current float64, now time.Time) (*WindowForecast, error) {
	rows, err := tx.Query(`SELECT sampled_at, used_percent FROM window_samples WHERE window_id=? AND reset_epoch=? AND sampled_at>=? ORDER BY sampled_at,id`, windowID, epoch, now.Add(-24*time.Hour).UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("store: query forecast samples: %w", err)
	}
	defer rows.Close()
	var samples []usageSample
	for rows.Next() {
		var raw string
		var used float64
		if err := rows.Scan(&raw, &used); err != nil {
			return nil, fmt.Errorf("store: scan forecast sample: %w", err)
		}
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return nil, fmt.Errorf("store: parse forecast sample: %w", err)
		}
		// Only the current monotonic segment is predictive after an observed reset.
		if len(samples) > 0 && used < samples[len(samples)-1].used {
			samples = samples[:0]
		}
		samples = append(samples, usageSample{at: at, used: used})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(samples) < 3 {
		return nil, nil
	}
	span := samples[len(samples)-1].at.Sub(samples[0].at)
	if span < 30*time.Minute || samples[len(samples)-1].used-samples[0].used < 1 {
		return nil, nil
	}
	base := samples[0].at
	var sx, sy, sxx, sxy float64
	for _, sample := range samples {
		x := sample.at.Sub(base).Hours()
		sx += x
		sy += sample.used
		sxx += x * x
		sxy += x * sample.used
	}
	n := float64(len(samples))
	denom := n*sxx - sx*sx
	if math.Abs(denom) < 1e-9 {
		return nil, nil
	}
	slope := (n*sxy - sx*sy) / denom
	if slope <= 0 || current >= 100 {
		return nil, nil
	}
	estimated := now.Add(time.Duration(((100 - current) / slope) * float64(time.Hour)))
	return &WindowForecast{
		ComputedAt: now.UTC(), BurnRatePercentPerHour: slope, EstimatedExhaustionAt: estimated.UTC(),
		ExhaustsBeforeReset: estimated.Before(resetsAt), SampleCount: len(samples), BasedOnHours: span.Hours(),
	}, nil
}
