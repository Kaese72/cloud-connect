-- Singleton-by-convention: application logic always reads the most recently
-- inserted row (ORDER BY id DESC LIMIT 1) as "the current enrollment state".
-- Every transition (enroll, reset) inserts a fresh row rather than mutating
-- one in place, so past enrollments remain auditable.
CREATE TABLE IF NOT EXISTS enrollment (
    id SERIAL PRIMARY KEY,
    applianceId BIGINT UNSIGNED NULL,
    applianceSecret VARCHAR(255) NULL,
    serverUrl VARCHAR(255) NULL,
    hostname VARCHAR(255) NULL,
    status ENUM('unenrolled', 'enrolled') NOT NULL DEFAULT 'unenrolled',
    enrolledAt TIMESTAMP NULL,
    createdAt TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Pending OAuth-style `state` values from POST .../enrollment/start, each
-- redeemed at most once by POST .../enrollment/complete before their short
-- TTL expires.
CREATE TABLE IF NOT EXISTS enrollmentStates (
    state CHAR(64) NOT NULL PRIMARY KEY,
    returnTo VARCHAR(512) NOT NULL,
    expiresAt TIMESTAMP NOT NULL,
    createdAt TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
