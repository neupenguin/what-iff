import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Reporter, TestCase, TestResult } from '@playwright/test/reporter';

/**
 * Collects the eval-side signals per agent-experience level and writes them
 * next to the agent results, where summarize.ts joins them in:
 *
 *   results/functional-by-level.json  — pass rate of the memory functional specs per level
 *   results/axe-by-level.json         — axe violation counts per level and state
 *
 * A project's level comes from its `metadata.axLevel`, its kind from
 * `metadata.axKind` ('functional' | 'axe'). Levels not run this time are kept
 * from the previous file, so the two kinds (or single levels) can be run
 * separately.
 */

interface FunctionalTally {
  passed: number;
  failed: number;
  skipped: number;
  total: number;
  passRate: number;
  failedTitles: string[];
}

interface AxeSummary {
  level: number;
  scans: { state: string; violationRules: number; violationNodes: number; byRule: Record<string, unknown> }[];
}

export default class AxLevelReporter implements Reporter {
  private readonly functional = new Map<number, FunctionalTally>();
  private readonly axe = new Map<number, AxeSummary>();
  private readonly outDir: string;

  constructor(options: { outDir?: string } = {}) {
    this.outDir = options.outDir ?? join(__dirname, 'results');
  }

  onTestEnd(test: TestCase, result: TestResult): void {
    // Only the final attempt counts when retries are on.
    if (result.retry < test.retries && result.status !== 'passed' && result.status !== 'skipped') return;
    const project = test.parent.project();
    const level = Number(project?.metadata?.['axLevel']);
    const kind = project?.metadata?.['axKind'];
    if (!Number.isInteger(level)) return;

    if (kind === 'functional') {
      const tally = this.functional.get(level) ?? { passed: 0, failed: 0, skipped: 0, total: 0, passRate: 0, failedTitles: [] };
      if (result.status === 'skipped') {
        tally.skipped++;
      } else {
        tally.total++;
        if (result.status === 'passed') {
          tally.passed++;
        } else {
          tally.failed++;
          tally.failedTitles.push(test.title);
        }
      }
      tally.passRate = tally.total > 0 ? tally.passed / tally.total : 0;
      this.functional.set(level, tally);
    }

    if (kind === 'axe') {
      const attachment = result.attachments.find(candidate => candidate.name === 'axe-summary' && candidate.body);
      if (attachment?.body) this.axe.set(level, JSON.parse(attachment.body.toString()) as AxeSummary);
    }
  }

  onEnd(): void {
    mkdirSync(this.outDir, { recursive: true });
    if (this.functional.size > 0) {
      this.merge('functional-by-level.json', Object.fromEntries(this.functional));
    }
    if (this.axe.size > 0) {
      const levels = Object.fromEntries(
        [...this.axe.entries()].map(([level, summary]) => [
          level,
          {
            totalViolationNodes: summary.scans.reduce((sum, scan) => sum + scan.violationNodes, 0),
            totalViolationRules: new Set(summary.scans.flatMap(scan => Object.keys(scan.byRule))).size,
            scans: summary.scans,
          },
        ]),
      );
      this.merge('axe-by-level.json', levels);
    }
  }

  printsToStdio(): boolean {
    return false;
  }

  private merge(file: string, levels: Record<string, unknown>): void {
    const path = join(this.outDir, file);
    const previous = existsSync(path) ? (JSON.parse(readFileSync(path, 'utf8')) as { levels?: Record<string, unknown> }).levels ?? {} : {};
    writeFileSync(path, JSON.stringify({ generatedAt: new Date().toISOString(), levels: { ...previous, ...levels } }, null, 2));
    console.log(`ax-level-reporter: wrote ${path}`);
  }
}
