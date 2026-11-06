package store

import (
	"database/sql"
	"errors"
	"time"
)

// TrainingLogEntry is one row of training_log. Markers (checkpoints) have no notification, no prediction and no delta.
type TrainingLogEntry struct {
	ID             int64
	NotificationID int64 // 0 for none
	Label          string
	Predicted      float64
	Delta          []byte
	CreatedAt      time.Time
}

// maxWeightSnapshots bounds how many past weight snapshots are kept per feature version.
const maxWeightSnapshots = 20

// LoadWeights returns the newest serialised weights saved for a feature version.
func (s *Store) LoadWeights(featureVersion string) ([]byte, bool, error) {
	var blob []byte
	err := s.db.QueryRow(`SELECT weights_blob FROM model_weights WHERE feature_version = ? ORDER BY id DESC LIMIT 1`, featureVersion).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return blob, err == nil, err
}

// UpdateModel saves a new weights snapshot (keeping the most recent few), removes the given training log rows and appends new
// ones, all in one transaction.
func (s *Store) UpdateModel(featureVersion string, weights []byte, add []TrainingLogEntry, removeIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO model_weights (feature_version, weights_blob) VALUES (?, ?)`, featureVersion, weights); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM model_weights WHERE feature_version = ?
		AND id NOT IN (SELECT id FROM model_weights WHERE feature_version = ? ORDER BY id DESC LIMIT ?)`,
		featureVersion, featureVersion, maxWeightSnapshots); err != nil {
		return err
	}
	for _, id := range removeIDs {
		if _, err := tx.Exec(`DELETE FROM training_log WHERE id = ?`, id); err != nil {
			return err
		}
	}
	for _, e := range add {
		if _, err := tx.Exec(`INSERT INTO training_log (notification_id, label, predicted, delta_blob) VALUES (?, ?, ?, ?)`,
			nullID(e.NotificationID), e.Label, nullPredicted(e), e.Delta); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func nullPredicted(e TrainingLogEntry) any {
	if e.Delta == nil && e.NotificationID == 0 {
		return nil
	}
	return e.Predicted
}

// TrainingLog returns the newest entries first; limit 0 means all.
func (s *Store) TrainingLog(limit int) ([]TrainingLogEntry, error) {
	query := `SELECT id, notification_id, label, predicted, delta_blob, CAST(created_at AS TEXT) FROM training_log ORDER BY id DESC`
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrainingLogEntry
	for rows.Next() {
		var (
			e         TrainingLogEntry
			notif     sql.NullInt64
			predicted sql.NullFloat64
			created   string
		)
		if err := rows.Scan(&e.ID, &notif, &e.Label, &predicted, &e.Delta, &created); err != nil {
			return nil, err
		}
		e.NotificationID, e.Predicted = notif.Int64, predicted.Float64
		if e.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ClearTrainingLog forgets all undo history, for example when the feature version changes and the old deltas no longer apply.
func (s *Store) ClearTrainingLog() error {
	_, err := s.db.Exec(`DELETE FROM training_log`)
	return err
}
