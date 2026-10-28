import { z } from 'zod'

// Validation rules shared by the forms. Each mirrors a rule the server
// enforces — the server stays the authority, so keep the values in sync.

// MIN_PASSWORD_LENGTH mirrors auth.MinPasswordLen on the server.
export const MIN_PASSWORD_LENGTH = 8

// passwordSchema is the rule for every new local account password.
export const passwordSchema = z
  .string()
  .min(MIN_PASSWORD_LENGTH, `Password must be at least ${MIN_PASSWORD_LENGTH} characters`)

// integerInRange validates a number input holding a whole number between min
// and max. An empty input is an error rather than 0: on the settings pages 0
// disables a feature, so it must be typed on purpose.
export function integerInRange(min: number, max: number) {
  const message = `Must be a whole number between ${min} and ${max}`
  return z.preprocess(
    (v) => (v === '' || v === null || v === undefined ? undefined : Number(v)),
    z
      .number({ error: (issue) => (issue.input === undefined ? 'A value is required' : message) })
      .int(message)
      .min(min, message)
      .max(max, message),
  )
}
