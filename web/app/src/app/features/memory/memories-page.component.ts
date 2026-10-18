import { ChangeDetectionStrategy, Component, OnInit, inject, signal } from '@angular/core';
import { ActivatedRoute, Router } from '@angular/router';

import { AxLevelService } from '../../core/services/ax-level.service';

import { MemoriesListTabComponent } from './memories-list-tab.component';
import { MemoryMergeHistoryPageComponent } from './memory-merge-history-page.component';
import { CompactionLogPageComponent } from './compaction-log-page.component';
import { HelpHintComponent } from '../../shared/ui/help-hint/help-hint.component';
import { TooltipDirective } from '../../shared/ui/tooltip/tooltip.directive';

export type MemoriesPageTab = 'memories' | 'merge-history' | 'compaction-log';

const TABS: ReadonlyArray<{ key: MemoriesPageTab; label: string; hint: string }> = [
  { key: 'memories', label: 'Memories', hint: '' },
  { key: 'merge-history', label: 'Merge history', hint: 'Automatic merges and links between duplicate or related memories' },
  { key: 'compaction-log', label: 'Compaction log', hint: 'What each thread checkpoint changed: summaries, scratchpads and memories' },
];

@Component({
  selector: 'app-memories-page',
  standalone: true,
  imports: [MemoriesListTabComponent, MemoryMergeHistoryPageComponent, CompactionLogPageComponent, HelpHintComponent, TooltipDirective],
  templateUrl: './memories-page.component.html',
  styleUrl: './memories-page.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class MemoriesPageComponent implements OnInit {
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  /** Agent-experience demo level; see AxLevelService. */
  readonly ax = inject(AxLevelService);
  readonly tabs = TABS;

  readonly activeTab = signal<MemoriesPageTab>('memories');

  ngOnInit(): void {
    this.route.queryParams.subscribe(params => {
      this.activeTab.set(normalizeTab(params['tab']));
    });
  }

  /** At ax level 3 the class names carry a per-load suffix; the stylesheet matches both spellings. */
  tabClasses(tab: MemoriesPageTab): string {
    const classes = [this.ax.cls('memories-shell__tab')];
    if (this.activeTab() === tab) classes.push(this.ax.cls('memories-shell__tab--active'));
    return classes.join(' ');
  }

  setActiveTab(tab: MemoriesPageTab): void {
    this.activeTab.set(tab);
    void this.router.navigate([], {
      relativeTo: this.route,
      queryParams: tab === 'memories' ? { tab: null } : { tab },
      queryParamsHandling: 'merge',
      replaceUrl: true,
    });
  }
}

function normalizeTab(raw: unknown): MemoriesPageTab {
  if (raw === 'merge-history' || raw === 'compaction-log') return raw;
  return 'memories';
}
