import { ChangeDetectionStrategy, Component, computed, DestroyRef, inject, input, OnInit, output, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { catchError, map, of } from 'rxjs';

import { Chat, isSandboxed } from '../../../core/models/chat.model';
import { Memory } from '../../../core/models/memory.model';
import { ChatService } from '../../../core/services/chat.service';
import { MemoryViewService } from '../../../core/services/memory-view.service';
import { ModalComponent } from '../../../shared/ui/modal/modal.component';

/** Threads offered as a destination; the list is for picking, not browsing. */
const THREAD_PICKER_LIMIT = 100;

/**
 * Moves one or more memories into a thread (POST /memory/{id}/scope with level `thread`), from
 * any level: global, personality, or another thread. Opened from the memory list's Move menu,
 * which handles global and personality destinations itself.
 *
 * For a single global/personality memory, the thread it came from (its `chat_id`) is listed first
 * and preselected. Otherwise nothing is preselected and the user picks: a memory is never attached
 * to a thread it was not explicitly sent to.
 *
 * Sandboxed (Discord relay) threads are offered only as the place a single memory came from; the
 * server (datastore.MoveMemoryScope) refuses any other move into one with a 409, whose reason is
 * shown as is. A rule change belongs in both places and in both test suites.
 */
@Component({
  selector: 'app-memory-scope-dialog',
  standalone: true,
  imports: [FormsModule, ModalComponent],
  templateUrl: './memory-scope-dialog.component.html',
  styleUrl: './memory-scope-dialog.component.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class MemoryScopeDialogComponent implements OnInit {
  private readonly view = inject(MemoryViewService);
  private readonly chatService = inject(ChatService);
  private readonly destroyRef = inject(DestroyRef);

  readonly memories = input.required<Memory[]>();

  /** Emitted once any memory has moved (all of them, or the rest after a partial failure was shown). */
  readonly moved = output<void>();
  readonly cancel = output<void>();

  readonly threads = signal<Chat[]>([]);
  readonly loading = signal(true);
  readonly saving = signal(false);
  /** Reasons the server gave for memories it would not move; the others have moved. */
  readonly errors = signal<string[]>([]);
  readonly threadId = signal<string | null>(null);

  /** The memory being moved, when there is exactly one. */
  readonly single = computed(() => {
    const memories = this.memories();
    return memories.length === 1 ? memories[0] : null;
  });
  /** The thread a single global/personality memory came from, when it still exists. */
  readonly returnThread = computed(() => {
    const memory = this.single();
    if (!memory?.chat_id || memory.level === 'thread') return null;
    return this.threads().find(chat => chat.id === memory.chat_id) ?? null;
  });
  /** Destination threads: the source thread first; sandboxes only take back their own; not a lone thread memory's current thread. */
  readonly threadOptions = computed(() => {
    const back = this.returnThread();
    const memory = this.single();
    const current = memory?.level === 'thread' ? memory.chat_id : undefined;
    const rest = this.threads().filter(chat => chat.id !== back?.id && chat.id !== current && !isSandboxed(chat));
    return back ? [back, ...rest] : rest;
  });
  readonly title = computed(() => {
    const count = this.memories().length;
    return count === 1 ? 'Move this memory to a thread' : `Move ${count} memories to a thread`;
  });
  readonly canSubmit = computed(() => !this.saving() && !this.loading() && this.threadId() !== null && this.errors().length === 0);

  ngOnInit(): void {
    const sourceID = this.single()?.chat_id;
    this.chatService
      .listChatsPage(1, THREAD_PICKER_LIMIT)
      .pipe(
        map(page => page.results ?? []),
        catchError(() => of<Chat[]>([])),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe(threads => {
        // The listing may stop before the source thread; an explicit id also finds an archived one.
        if (sourceID && !threads.some(chat => chat.id === sourceID)) {
          this.loadSourceThread(sourceID, threads);
          return;
        }
        this.ready(threads);
      });
  }

  submit(): void {
    const chatID = this.threadId();
    if (!chatID || !this.canSubmit()) return;
    this.saving.set(true);
    const ids = this.memories().map(memory => memory.id);
    this.view.moveScope(ids, { level: 'thread', chat_id: chatID }).subscribe({
      next: reasons => {
        this.saving.set(false);
        if (reasons.length === 0) {
          this.moved.emit();
          return;
        }
        this.errors.set(reasons);
      },
      // moveScope reports each refusal as a reason; this is only for a failure outside the requests.
      error: () => {
        this.saving.set(false);
        this.errors.set(['Failed to move memories']);
      },
    });
  }

  /** Closing after a partial failure still reports the moves that went through. */
  close(): void {
    const someMoved = this.errors().length > 0 && this.errors().length < this.memories().length;
    if (someMoved) {
      this.moved.emit();
    } else {
      this.cancel.emit();
    }
  }

  private loadSourceThread(sourceID: string, threads: Chat[]): void {
    this.chatService
      .listChatsPage(1, 1, { ids: sourceID })
      .pipe(
        map(page => page.results?.find(chat => chat.id === sourceID) ?? null),
        catchError(() => of(null)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe(source => this.ready(source ? [source, ...threads] : threads));
  }

  private ready(threads: Chat[]): void {
    this.threads.set(threads);
    this.threadId.set(this.returnThread()?.id ?? null);
    this.loading.set(false);
  }
}
