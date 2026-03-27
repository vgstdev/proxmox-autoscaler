// Copyright (c) 2026 VGS https://vgst.net
// SPDX-License-Identifier: MIT

package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS active_boosts (
    vmid           INTEGER NOT NULL,
    resource_type  TEXT    NOT NULL,
    original_value REAL    NOT NULL,
    boosted_value  REAL    NOT NULL,
    boost_factor   REAL    NOT NULL,
    boosted_at     TEXT    NOT NULL,
    PRIMARY KEY (vmid, resource_type)
)`

// BoostRecord represents a persisted boost entry.
type BoostRecord struct {
	VMID          int
	ResourceType  string
	OriginalValue float64
	BoostedValue  float64
	BoostFactor   float64
	BoostedAt     time.Time
}

// DB wraps a SQLite database connection.
type DB struct {
	conn *sql.DB
}

// Open opens (or creates) the SQLite database at path, ensuring the directory exists.
func Open(path string, logger *slog.Logger) (*DB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create db directory %q: %w", dir, err)
	}

	_, statErr := os.Stat(path)
	isNew := os.IsNotExist(statErr)

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db %q: %w", path, err)
	}

	conn.SetMaxOpenConns(1)

	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("apply db schema: %w", err)
	}

	if isNew {
		logger.Info("DB opened", "path", path, "status", "created new")
	} else {
		logger.Info("DB opened", "path", path, "status", "existing")
	}

	return &DB{conn: conn}, nil
}

// SaveBoost inserts or replaces a boost record.
func (d *DB) SaveBoost(rec BoostRecord) error {
	const q = `
INSERT OR REPLACE INTO active_boosts
    (vmid, resource_type, original_value, boosted_value, boost_factor, boosted_at)
VALUES (?, ?, ?, ?, ?, ?)`

	_, err := d.conn.Exec(q,
		rec.VMID,
		rec.ResourceType,
		rec.OriginalValue,
		rec.BoostedValue,
		rec.BoostFactor,
		rec.BoostedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("save boost vmid=%d resource=%s: %w", rec.VMID, rec.ResourceType, err)
	}
	return nil
}

// DeleteBoost removes a boost record.
func (d *DB) DeleteBoost(vmid int, resourceType string) error {
	const q = `DELETE FROM active_boosts WHERE vmid = ? AND resource_type = ?`
	_, err := d.conn.Exec(q, vmid, resourceType)
	if err != nil {
		return fmt.Errorf("delete boost vmid=%d resource=%s: %w", vmid, resourceType, err)
	}
	return nil
}

// LoadAllBoosts returns all persisted boost records.
func (d *DB) LoadAllBoosts() ([]BoostRecord, error) {
	const q = `SELECT vmid, resource_type, original_value, boosted_value, boost_factor, boosted_at FROM active_boosts`

	rows, err := d.conn.Query(q)
	if err != nil {
		return nil, fmt.Errorf("load boosts: %w", err)
	}
	defer rows.Close()

	var records []BoostRecord
	for rows.Next() {
		var rec BoostRecord
		var boostedAtStr string
		if err := rows.Scan(
			&rec.VMID,
			&rec.ResourceType,
			&rec.OriginalValue,
			&rec.BoostedValue,
			&rec.BoostFactor,
			&boostedAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan boost row: %w", err)
		}
		t, err := time.Parse(time.RFC3339, boostedAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse boosted_at %q: %w", boostedAtStr, err)
		}
		rec.BoostedAt = t
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate boost rows: %w", err)
	}
	return records, nil
}

// Close closes the underlying database connection.
func (d *DB) Close() error {
	return d.conn.Close()
}
