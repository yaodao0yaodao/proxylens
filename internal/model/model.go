package model

import "time"

type Task struct {
	ID                      string    `json:"id"`
	Name                    string    `json:"name"`
	SubscriptionURL         string    `json:"subscription_url,omitempty"`
	SubscriptionUA          string    `json:"subscription_user_agent"`
	PublishToken            string    `json:"publish_token,omitempty"`
	Enabled                 bool      `json:"enabled"`
	LastSubscriptionAt      time.Time `json:"last_subscription_at,omitempty"`
	SubscriptionAttemptAt   time.Time `json:"subscription_attempt_at,omitempty"`
	SubscriptionFailedSince time.Time `json:"subscription_failed_since,omitempty"`
	LastRulesAt             time.Time `json:"last_rules_at,omitempty"`
	LastSFAAt               time.Time `json:"last_sfa_at,omitempty"`
	LastCartonAt            time.Time `json:"last_carton_at,omitempty"`
	LastDetectionAt         time.Time `json:"last_detection_at,omitempty"`
	LastError               string    `json:"last_error,omitempty"`
	SubscriptionUpload      int64     `json:"subscription_upload,omitempty"`
	SubscriptionDownload    int64     `json:"subscription_download,omitempty"`
	SubscriptionTotal       int64     `json:"subscription_total,omitempty"`
	SubscriptionExpire      time.Time `json:"subscription_expire,omitempty"`
	SubscriptionInfoAt      time.Time `json:"subscription_info_at,omitempty"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type Node struct {
	ID             string         `json:"id"`
	TaskID         string         `json:"task_id"`
	Number         int64          `json:"number"`
	Protocol       string         `json:"protocol"`
	Server         string         `json:"server"`
	Port           int            `json:"port"`
	OriginalName   string         `json:"original_name"`
	DisplayName    string         `json:"display_name"`
	Multiplier     float64        `json:"multiplier"`
	ASN            string         `json:"asn,omitempty"`
	ASNServer      string         `json:"asn_server,omitempty"`
	ExitIP         string         `json:"exit_ip,omitempty"`
	CountryCode    string         `json:"country_code,omitempty"`
	Country        string         `json:"country,omitempty"`
	Config         map[string]any `json:"config,omitempty"`
	ConfigRevision string         `json:"config_revision,omitempty"`
	AddedAt        time.Time      `json:"added_at"`
	ModifiedAt     time.Time      `json:"modified_at"`
	RemovedAt      *time.Time     `json:"removed_at,omitempty"`
}

type IdentityConflict struct {
	ID           int64               `json:"id"`
	TaskID       string              `json:"task_id"`
	IncomingID   string              `json:"incoming_id"`
	IncomingName string              `json:"incoming_name"`
	Incoming     *IdentityCandidate  `json:"incoming,omitempty"`
	CandidateIDs []string            `json:"candidate_ids"`
	Candidates   []IdentityCandidate `json:"candidates,omitempty"`
	Reason       string              `json:"reason"`
	DetectedAt   time.Time           `json:"detected_at"`
	ResolvedAt   *time.Time          `json:"resolved_at,omitempty"`
}

type IdentityCandidate struct {
	ID           string     `json:"id"`
	Number       int64      `json:"number"`
	Protocol     string     `json:"protocol"`
	DisplayName  string     `json:"display_name,omitempty"`
	OriginalName string     `json:"original_name"`
	Server       string     `json:"server"`
	Port         int        `json:"port"`
	Multiplier   float64    `json:"multiplier"`
	ASN          string     `json:"asn,omitempty"`
	ExitIP       string     `json:"exit_ip,omitempty"`
	CountryCode  string     `json:"country_code,omitempty"`
	Country      string     `json:"country,omitempty"`
	AddedAt      time.Time  `json:"added_at"`
	ModifiedAt   time.Time  `json:"modified_at"`
	RemovedAt    *time.Time `json:"removed_at,omitempty"`
	Removed      bool       `json:"removed"`
}

type Notice struct {
	Name string `json:"name"`
}

type Measurement struct {
	ID              int64     `json:"id"`
	NodeID          string    `json:"node_id"`
	Kind            string    `json:"kind"`
	Available       *bool     `json:"available,omitempty"`
	LatencyMS       *float64  `json:"latency_ms,omitempty"`
	DirectLatencyMS *float64  `json:"direct_latency_ms,omitempty"`
	DownloadBytes   int64     `json:"download_bytes,omitempty"`
	ExitIP          string    `json:"exit_ip,omitempty"`
	ExitError       string    `json:"exit_error,omitempty"`
	Error           string    `json:"error,omitempty"`
	TestedAt        time.Time `json:"tested_at"`
	ConfigRevision  string    `json:"config_revision,omitempty"`
	SampleCount     int       `json:"sample_count,omitempty"`
	AvailableCount  int       `json:"available_count,omitempty"`
}

type Quality struct {
	NodeID            string          `json:"node_id"`
	Availability      float64         `json:"availability"`
	AvailabilityRaw   float64         `json:"availability_raw"`
	Samples           int             `json:"samples"`
	UnavailableByHour map[int]float64 `json:"unavailable_by_hour"`
	AverageLatencyMS  float64         `json:"average_latency_ms"`
	Priority          float64         `json:"priority"`
	CalculatedAt      time.Time       `json:"calculated_at"`
}

type Schedule struct {
	DetectionInterval time.Duration
	RulesInterval     time.Duration
}
