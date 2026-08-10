/**
 * The encryption choices the host/port connection form offers, per driver.
 *
 * The list used to be one array shared by every driver, which read well but was
 * not true: `require` means "encrypt, don't check who answered" in postgres and
 * "encrypt and verify the certificate chain and the address" in the mysql
 * driver. A tenant pointing the form at a mysql box with the certificate mysqld
 * generates for itself picked "Require TLS (recommended)" and got
 *
 *   x509: cannot validate certificate for 10.0.0.4 because it doesn't contain
 *   any IP SANs
 *
 * with nothing on the form to choose instead. So mysql does not offer `require`
 * at all — its two meanings are split into the choices that say which one they
 * are — and `skip-verify` exists to name the middle ground the driver has and
 * the form did not: encrypted, unverified, no silent fall back to plaintext.
 */

export type SslModeOption = {
  value: string
  label: string
}

/** postgres spells the modes the same way libpq does, so its list is the
 *  driver's own vocabulary and needs no reconciling. */
const POSTGRES_MODES: SslModeOption[] = [
  { value: 'require', label: 'Require TLS (recommended)' },
  { value: 'verify-full', label: 'Require TLS and verify the certificate' },
  { value: 'prefer', label: 'Use TLS if the server offers it' },
  { value: 'disable', label: 'No TLS — only on a trusted network' },
]

/** mysql omits `require` deliberately: in go-sql-driver `tls=true` verifies the
 *  chain and the address, which is what `verify-full` says out loud. */
const MYSQL_MODES: SslModeOption[] = [
  { value: 'verify-full', label: 'Require TLS and verify the certificate (recommended)' },
  { value: 'skip-verify', label: "Require TLS, don't verify the certificate" },
  { value: 'prefer', label: 'Use TLS if the server offers it' },
  { value: 'disable', label: 'No TLS — only on a trusted network' },
]

const MODES_BY_DRIVER: Record<string, SslModeOption[]> = {
  postgres: POSTGRES_MODES,
  mysql: MYSQL_MODES,
}

/**
 * sslModeOptions returns the choices a driver has. It is empty for drivers the
 * control does not apply to — SQL Server sets its own encryption parameters in
 * the backend's buildDSN — and callers hide the field when it is.
 */
export function sslModeOptions(dbType: string): SslModeOption[] {
  return MODES_BY_DRIVER[dbType] ?? []
}

/** supportsSslMode is sslModeOptions read as a question. */
export function supportsSslMode(dbType: string): boolean {
  return sslModeOptions(dbType).length > 0
}

/**
 * defaultSslMode is the strictest choice a driver has, so the form's untouched
 * state is the safe one and every step down from it is a decision someone made.
 * It matches what the backend does with an unset mode.
 */
export function defaultSslMode(dbType: string): string {
  const options = sslModeOptions(dbType)
  if (options.length === 0) return ''
  return dbType === 'mysql' ? 'verify-full' : 'require'
}

/** A driver's TLS handshake refusing the certificate it was shown. The three
 *  patterns are what postgres, mysql and sqlserver each call it. */
const CERT_ERROR_RE = /x509|certificate|tls: |ssl.*(verify|handshake)/i

/**
 * certVerificationHint turns a failed connection test into the next thing to
 * try, when — and only when — the failure was the certificate rather than the
 * host, the port or the password. The driver's own message is accurate and
 * unreadable: it names an x509 extension, not a control on this form.
 */
export function certVerificationHint(error: string | null | undefined, dbType: string): string | null {
  if (!error || !CERT_ERROR_RE.test(error)) return null
  if (!supportsSslMode(dbType)) return null
  return (
    "The database answered, but its TLS certificate could not be verified — it is usually self-signed, " +
    'or issued for a hostname while you connected by IP. If you trust the network path to this server, ' +
    'set Encryption to a mode that does not verify the certificate. Otherwise install a certificate that ' +
    'covers this address and keep verification on.'
  )
}
