import { TestBed } from '@angular/core/testing';
import { provideZonelessChangeDetection } from '@angular/core';

import { AX_LEVEL_STORAGE_KEY, AxLevelService, parseAxLevel, readInitialLevel } from './ax-level.service';

describe('AxLevelService', () => {
  const originalUrl = window.location.href;

  function visit(search: string): void {
    window.history.replaceState(null, '', `/memories${search}`);
  }

  beforeEach(() => {
    sessionStorage.removeItem(AX_LEVEL_STORAGE_KEY);
    localStorage.removeItem(AX_LEVEL_STORAGE_KEY);
    visit('');
  });

  afterEach(() => {
    sessionStorage.removeItem(AX_LEVEL_STORAGE_KEY);
    localStorage.removeItem(AX_LEVEL_STORAGE_KEY);
    window.history.replaceState(null, '', originalUrl);
  });

  it('parses only 0-3', () => {
    expect(parseAxLevel('2')).toBe(2);
    expect(parseAxLevel('0')).toBe(0);
    expect(parseAxLevel('4')).toBeNull();
    expect(parseAxLevel('x')).toBeNull();
    expect(parseAxLevel(null)).toBeNull();
  });

  it('defaults to level 0', () => {
    expect(readInitialLevel()).toBe(0);
  });

  it('reads ?ax=N and keeps it in sessionStorage for later navigations', () => {
    visit('?ax=2');
    expect(readInitialLevel()).toBe(2);
    expect(sessionStorage.getItem(AX_LEVEL_STORAGE_KEY)).toBe('2');

    visit('');
    expect(readInitialLevel()).toBe(2);
  });

  it('clears the stored level on ?ax=0', () => {
    sessionStorage.setItem(AX_LEVEL_STORAGE_KEY, '3');
    visit('?ax=0');
    expect(readInitialLevel()).toBe(0);
    expect(sessionStorage.getItem(AX_LEVEL_STORAGE_KEY)).toBeNull();
  });

  it('falls back to a level pinned in localStorage', () => {
    localStorage.setItem(AX_LEVEL_STORAGE_KEY, '1');
    expect(readInitialLevel()).toBe(1);
  });

  it('gates names, roles and state cumulatively and suffixes classes only at level 3', () => {
    TestBed.configureTestingModule({ providers: [provideZonelessChangeDetection()] });
    const ax = TestBed.inject(AxLevelService);

    ax.setLevel(0);
    expect(ax.name('Close')).toBe('Close');
    expect(ax.role('dialog')).toBe('dialog');
    expect(ax.state(true)).toBe(true);
    expect(ax.cls('memory-card')).toBe('memory-card');

    ax.setLevel(1);
    expect(ax.name('Close')).toBeNull();
    expect(ax.role('dialog')).toBe('dialog');

    ax.setLevel(2);
    expect(ax.role('dialog')).toBeNull();
    expect(ax.state(true)).toBe(true);

    ax.setLevel(3);
    expect(ax.state(true)).toBeNull();
    expect(ax.cls('memory-card')).toBe(`memory-card-ax${ax.suffix}`);
    expect(ax.suffix).toMatch(/^[a-z0-9]{6}$/);
  });
});
