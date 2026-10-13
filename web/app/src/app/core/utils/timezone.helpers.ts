import type { AbstractControl, ValidationErrors } from '@angular/forms';

/** Longest timezone string the profile API accepts (matches the /profile page). */
export const TIMEZONE_MAX_LENGTH = 64;

const FALLBACK_TIMEZONES = [
  'America/New_York',
  'America/Los_Angeles',
  'America/Chicago',
  'Europe/London',
  'Europe/Paris',
  'Asia/Tokyo',
  'UTC',
];

/** IANA timezone identifiers for pickers, sorted. Always includes UTC. */
export function listTimezones(): string[] {
  try {
    const supported =
      typeof Intl !== 'undefined' && typeof Intl.supportedValuesOf === 'function'
        ? Intl.supportedValuesOf('timeZone')
        : FALLBACK_TIMEZONES;
    const list = Array.isArray(supported) && supported.length > 0 ? [...supported] : [...FALLBACK_TIMEZONES];
    if (!list.includes('UTC')) list.push('UTC');
    return list.sort();
  } catch {
    return [...FALLBACK_TIMEZONES].sort();
  }
}

/**
 * The browser's IANA timezone, or '' when it cannot be determined or is not a usable zone name
 * (some runtimes report placeholders such as `Etc/Unknown`). Never throws.
 */
export function detectBrowserTimezone(): string {
  try {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone || '';
    return zone && isValidTimezone(zone) ? zone : '';
  } catch {
    return '';
  }
}

/**
 * Timezone to prefill new jobs with: the user's saved timezone, else the
 * browser-detected one, else UTC.
 */
export function resolveDefaultTimezone(saved?: string | null): string {
  return (saved ?? '').trim() || detectBrowserTimezone() || 'UTC';
}

/**
 * True when the runtime accepts `tz` as an IANA timezone identifier. A runtime without
 * `Intl.DateTimeFormat` can't check, so it accepts the name rather than rejecting every zone; the
 * profile API validates it on save either way.
 */
export function isValidTimezone(tz: string): boolean {
  if (typeof Intl === 'undefined' || typeof Intl.DateTimeFormat !== 'function') return true;
  try {
    new Intl.DateTimeFormat(undefined, { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

/** Reactive-forms validator: empty is allowed (means "unset"); otherwise must be a real IANA name. */
export function timezoneValidator(control: AbstractControl): ValidationErrors | null {
  const value = String(control.value ?? '').trim();
  if (!value) return null;
  return isValidTimezone(value) ? null : { timezone: true };
}
