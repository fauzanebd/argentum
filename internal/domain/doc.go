// Package domain holds the pure business entities and repository interfaces
// for Argentum.
//
// Rules for code in this package:
//
//   - No imports from other internal/* packages.
//   - No HTTP, no database/sql, no third-party clients.
//   - Repository interfaces declared here are implemented under internal/adapters/.
//   - Use cases / services that orchestrate repositories live under internal/app/.
//
// This package is the inner ring of the architecture. Everything else depends
// on it; it depends on nothing application-specific.
package domain
