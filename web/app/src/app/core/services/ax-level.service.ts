import { Injectable, computed, signal } from '@angular/core';

/**
 * Accessibility degradation level for the agent-experience demo (`demo/ax-levels`).
 *
 * - 0: the app as shipped.
 * - 1: no names — aria-labels and label/control associations are dropped.
 * - 2: no roles — interactive elements become plain clickable divs, landmark/list/dialog roles go.
 * - 3: no state — aria-selected/aria-expanded and live-region roles go, and key class names get a
 *      per-load random suffix so selector scripts written against one load break on the next.
 *
 * Levels degrade semantics only. Every level must render the same pixels; templates swap elements
 * and attributes, never styling. Only features/memory (and ui-modal when memory opens it) consult
 * this service.
 *
 * The level is read once at boot from `?ax=N` and kept in sessionStorage so in-app navigation and
 * the login redirect keep it. `?ax=0` clears a stored level. With neither, a level in localStorage
 * under the same key is used: that is how a Playwright `storageState` pins one level for a whole
 * functional-suite run (see e2e/ax-demo/playwright.config.ax-levels.ts) without touching the specs.
 */
export type AxLevel = 0 | 1 | 2 | 3;

export const AX_LEVEL_QUERY_PARAM = 'ax';
export const AX_LEVEL_STORAGE_KEY = 'whatiff.ax-level';

@Injectable({ providedIn: 'root' })
export class AxLevelService {
  private readonly levelState = signal<AxLevel>(readInitialLevel());

  readonly level = this.levelState.asReadonly();

  /** Random per page load; appended to key class names at level 3. */
  readonly suffix = randomSuffix();

  /** True while accessible names are exposed (level 0). */
  readonly names = computed(() => this.level() < 1);
  /** True while semantic roles and native interactive elements are used (levels 0–1). */
  readonly roles = computed(() => this.level() < 2);
  /** True while state attributes and live regions are exposed (levels 0–2). */
  readonly states = computed(() => this.level() < 3);

  /** `label` while names are exposed, otherwise null (removes the attribute). */
  name(label: string): string | null {
    return this.names() ? label : null;
  }

  /** `role` while roles are exposed, otherwise null. */
  role(role: string): string | null {
    return this.roles() ? role : null;
  }

  /** `value` while state is exposed, otherwise null. Use for aria-selected/expanded and live regions. */
  state<T>(value: T): T | null {
    return this.states() ? value : null;
  }

  /**
   * The class name to render for a key element. At level 3 the base name gets the per-load suffix;
   * stylesheets match both spellings with the `ax-class` SCSS function, so the visuals do not change.
   */
  cls(base: string): string {
    return this.level() >= 3 ? `${base}-ax${this.suffix}` : base;
  }

  /** For unit tests and the browser console; not persisted. */
  setLevel(level: AxLevel): void {
    this.levelState.set(level);
  }
}

export function parseAxLevel(raw: string | null | undefined): AxLevel | null {
  if (raw === null || raw === undefined) return null;
  const value = Number.parseInt(raw, 10);
  if (value === 0 || value === 1 || value === 2 || value === 3) return value;
  return null;
}

/** Exported for unit tests; the service calls it once, at construction. */
export function readInitialLevel(): AxLevel {
  if (typeof window === 'undefined') return 0;
  let fromQuery: AxLevel | null = null;
  try {
    fromQuery = parseAxLevel(new URLSearchParams(window.location.search).get(AX_LEVEL_QUERY_PARAM));
  } catch {
    fromQuery = null;
  }
  try {
    if (fromQuery !== null) {
      if (fromQuery === 0) {
        window.sessionStorage.removeItem(AX_LEVEL_STORAGE_KEY);
      } else {
        window.sessionStorage.setItem(AX_LEVEL_STORAGE_KEY, String(fromQuery));
      }
      return fromQuery;
    }
    return (
      parseAxLevel(window.sessionStorage.getItem(AX_LEVEL_STORAGE_KEY)) ??
      parseAxLevel(window.localStorage.getItem(AX_LEVEL_STORAGE_KEY)) ??
      0
    );
  } catch {
    return fromQuery ?? 0;
  }
}

function randomSuffix(): string {
  return Math.random().toString(36).slice(2, 8).padEnd(6, '0');
}
