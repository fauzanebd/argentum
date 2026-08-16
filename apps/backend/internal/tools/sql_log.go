package tools

import "strings"

// normalizeSQLForLog strips the data out of a statement so the shape can be
// logged and the values cannot (T-H7).
//
// **What it closes.** `run_sql` logged the executed SQL at Info with its
// literals intact, so a turn asking about one person put
// `WHERE nik = '3201234567890123'` into the operational log — and from there
// into wherever logs are shipped, retained and searched by people with no
// business reading a tenant's customers. The audit row already carries what an
// incident needs (`agent_actions.args_redacted`, which is written by a path
// that redacts); the log line was carrying it a second time, unredacted, at the
// level that is on in production.
//
// **Why normalise rather than drop the field.** The statement's shape is what
// an operator actually reads a query log for: which tables a tenant hits, how
// often, whether a turn is looping on the same query. None of that needs the
// values. So every literal becomes `?` and everything else survives — the same
// trade a database's own statement digest makes, and what `pg_query_go`'s
// `Normalize` would do for us once T-H4 brings it in. Until then this is a
// scanner rather than a parser, for the reason `parseEqualityFilters` gives one
// file over: what a parser buys is the predicates this misses, and missing one
// costs a `?` too many, never a leak.
//
// Rules, in one pass:
//   - a single-quoted string becomes `'?'`, doubled-quote escapes included;
//   - a number that stands on its own becomes `?` — a NIK, a phone number and
//     an account id are all numeric, and `col1`/`t2.x` are not touched because
//     the digits there follow an identifier character;
//   - `-- line` and `/* block */` comments are dropped entirely, because a
//     model that echoes the user's question into a comment would otherwise
//     write it to the log;
//   - double-quoted and backticked identifiers are kept verbatim: they are
//     names, and a table called "orders" is the part worth logging.
func normalizeSQLForLog(sql string) string {
	var b strings.Builder
	b.Grow(len(sql))

	for i := 0; i < len(sql); i++ {
		ch := sql[i]

		switch {
		case ch == '\'':
			// A string literal, however long, and whatever it holds.
			i++
			for i < len(sql) {
				if sql[i] == '\'' {
					if i+1 < len(sql) && sql[i+1] == '\'' {
						i += 2 // doubled-quote escape: still inside the literal
						continue
					}
					break
				}
				i++
			}
			b.WriteString("'?'")

		case ch == '"' || ch == '`':
			// A quoted identifier. Copied through, closing quote included.
			quote := ch
			b.WriteByte(ch)
			i++
			for i < len(sql) && sql[i] != quote {
				b.WriteByte(sql[i])
				i++
			}
			if i < len(sql) {
				b.WriteByte(quote)
			}

		case ch == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			// Keep the newline so a multi-line statement stays readable.
			if i < len(sql) {
				b.WriteByte('\n')
			}

		case ch == '/' && i+1 < len(sql) && sql[i+1] == '*':
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				i = len(sql) // unterminated: the rest is comment
			} else {
				i += 2 + end + 1
			}
			b.WriteByte(' ')

		case isDigit(ch) && !continuesIdentifier(lastByte(&b)):
			// A numeric literal, integer or decimal, with an optional exponent.
			for i < len(sql) && (isDigit(sql[i]) || sql[i] == '.') {
				i++
			}
			if i < len(sql) && (sql[i] == 'e' || sql[i] == 'E') {
				j := i + 1
				if j < len(sql) && (sql[j] == '+' || sql[j] == '-') {
					j++
				}
				if j < len(sql) && isDigit(sql[j]) {
					for j < len(sql) && isDigit(sql[j]) {
						j++
					}
					i = j
				}
			}
			i-- // the loop's own i++ consumes the byte that ended the number
			b.WriteByte('?')

		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// continuesIdentifier reports whether b can be part of an identifier, which is
// how `column1` and `t2.name` keep their digits while `= 42` loses them.
func continuesIdentifier(b byte) bool {
	return b == '_' || b == '.' || b == '$' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || isDigit(b)
}

// lastByte is the byte most recently written, or 0 at the start of the
// statement (where a digit is a literal, not the tail of a name).
func lastByte(b *strings.Builder) byte {
	s := b.String()
	if s == "" {
		return 0
	}
	return s[len(s)-1]
}
