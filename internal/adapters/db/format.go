package db

import (
	"fmt"
	"strings"
)

// FormatSchemaForPrompt renders SchemaMetadata as a compact human-readable
// block suitable for the agent's system prompt. Format intentionally matches
// the legacy metadata.SchemaManager.ToPromptFormat output so existing prompts
// continue to work.
func FormatSchemaForPrompt(s *SchemaMetadata) string {
	if s == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Database Schema:\n\n")
	for _, t := range s.Tables {
		sb.WriteString(fmt.Sprintf("Table: %s", t.Name))
		if t.Description != "" {
			sb.WriteString(" - " + t.Description)
		}
		sb.WriteString("\n")
		for _, c := range t.Columns {
			sb.WriteString(fmt.Sprintf("  • %s (%s)", c.Name, c.Type))
			if c.Description != "" {
				sb.WriteString(": " + c.Description)
			}
			if c.IsPrimaryKey {
				sb.WriteString(" [PK]")
			}
			if c.IsForeignKey {
				sb.WriteString(fmt.Sprintf(" → %s.%s", c.ForeignKeyTable, c.ForeignKeyColumn))
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	if len(s.Relationships) > 0 {
		sb.WriteString("Relationships:\n")
		for _, r := range s.Relationships {
			sb.WriteString(fmt.Sprintf("  • %s.%s → %s.%s\n",
				r.FromTable, r.FromColumn, r.ToTable, r.ToColumn))
		}
	}
	return sb.String()
}
