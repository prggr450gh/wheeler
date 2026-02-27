package models

import (
	"database/sql"
	"fmt"
)

type ConfigSetting struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

type ConfigService struct {
	db *sql.DB
}

func NewConfigService(db *sql.DB) *ConfigService {
	return &ConfigService{db: db}
}

func (s *ConfigService) GetAll() ([]*ConfigSetting, error) {
	rows, err := s.db.Query(`SELECT key, value, COALESCE(description, '') FROM config ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	defer rows.Close()

	var settings []*ConfigSetting
	for rows.Next() {
		var c ConfigSetting
		if err := rows.Scan(&c.Key, &c.Value, &c.Description); err != nil {
			return nil, fmt.Errorf("failed to scan config row: %w", err)
		}
		settings = append(settings, &c)
	}
	return settings, rows.Err()
}

func (s *ConfigService) GetValue(key, defaultVal string) string {
	var value string
	err := s.db.QueryRow(`SELECT value FROM config WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return defaultVal
	}
	return value
}

func (s *ConfigService) Set(key, value string) (*ConfigSetting, error) {
	_, err := s.db.Exec(`UPDATE config SET value = ? WHERE key = ?`, value, key)
	if err != nil {
		return nil, fmt.Errorf("failed to update config key %s: %w", key, err)
	}

	var c ConfigSetting
	err = s.db.QueryRow(`SELECT key, value, COALESCE(description, '') FROM config WHERE key = ?`, key).
		Scan(&c.Key, &c.Value, &c.Description)
	if err != nil {
		return nil, fmt.Errorf("failed to read config key %s after update: %w", key, err)
	}
	return &c, nil
}
