import { ChangeDetectionStrategy, Component, computed, DestroyRef, inject, input, OnInit, output, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { catchError, map, Observable, of, switchMap } from 'rxjs';

import { Chat, isSandboxed } from '../../../core/models/chat.model';
import { Memory, MemoryScopeMoveInput } from '../../../core/models/memory.model';
import { ChatService } from '../../../core/services/chat.service';
import { MemoryService } from '../../../core/services/memory.service';
import { apiErrorMessage } from '../../../core/utils/api-error.helpers';
import { ModalComponent } from '../../../shared/ui/modal/modal.component';
import { GLOBAL_SCOPE_LABEL } from '../helpers/memory-vm.helpers';
import { MemoryPersonalityOption } from './memory-form.component';

/** Threads offered as a destination; the list is for picking, not browsing. */
const THREAD_PICKER_LIMIT = 100;

/**
 * Moves one memory across the thread boundary (POST /memory/{id}/scope): a thread memory up to
 * global or its thread's personality, or a global/personality memory back to a thread. Moving
 * between global and personalities stays with the memory list's Move menu.
 *
 * Back to thread preselects the thread the memory came from (its `chat_id`) when that thread
 * still exists and belongs to the memory's personality. Otherwise nothing is preselected and the
 * user must pick one: a memory is never attached to a thread it was not explicitly sent to.
 *
 * The personality and sandbox rules here only shape what the dialog offers; the server
 * (datastore.MoveMemoryScope) is authoritative and refuses anything else with a 409, whose reason
 * is shown as is. A rule change belongs in both places and in both test suites.
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
  private readonly memoryService = inject(MemoryService);
  private readonly chatService = inject(ChatService);
  private readonly destroyRef = inject(DestroyRef);

  readonly memory = input.required<Memory>();
  readonly personalities = input<MemoryPersonalityOption[]>([]);

  readonly moved = output<Memory>();
  readonly cancel = output<void>();

  readonly globalScopeLabel = GLOBAL_SCOPE_LABEL;

  /** The memory's thread, or the thread it was promoted from; null when none is recorded or it was deleted. */
  readonly sourceThread = signal<Chat | null>(null);
  readonly threads = signal<Chat[]>([]);
  readonly loading = signal(true);
  readonly saving = signal(false);
  readonly error = signal<string | null>(null);
  /** Promotion target for a thread memory. */
  readonly promoteTo = signal<'global' | 'personality' | null>(null);
  /** Destination thread for a global/personality memory. */
  readonly threadId = signal<string | null>(null);

  readonly isThreadMemory = computed(() => this.memory().level === 'thread');
  /** The personality a move stays within: the memory's pin, else its source thread's. */
  readonly personalityId = computed(() => this.memory().pinned_personality_id ?? this.sourceThread()?.personality_id ?? null);
  readonly personalityName = computed(() => {
    const id = this.personalityId();
    if (!id) return null;
    return this.personalities().find(p => p.id === id)?.label ?? this.sourceThread()?.personality_name ?? null;
  });
  /** The source thread, when it is a valid destination for moving the memory back. */
  readonly returnThread = computed(() => {
    const source = this.sourceThread();
    if (!source) return null;
    const personalityId = this.personalityId();
    if (personalityId && source.personality_id !== personalityId) return null;
    return source;
  });
  /** Destination threads: the memory's personality's, the source thread first; sandboxes only take back their own. */
  readonly threadOptions = computed(() => {
    const back = this.returnThread();
    const rest = this.threads().filter(chat => chat.id !== back?.id && !isSandboxed(chat));
    return back ? [back, ...rest] : rest;
  });
  readonly canSubmit = computed(() => !this.saving() && !this.loading() && this.moveInput() !== null);

  ngOnInit(): void {
    const memory = this.memory();
    this.loadSourceThread(memory.chat_id)
      .pipe(
        switchMap(source => {
          this.sourceThread.set(source);
          return this.isThreadMemory() ? of<Chat[]>([]) : this.loadThreads(this.personalityId());
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe(threads => {
        this.threads.set(threads);
        this.threadId.set(this.returnThread()?.id ?? null);
        this.loading.set(false);
      });
  }

  submit(): void {
    const move = this.moveInput();
    if (!move || this.saving()) return;
    this.saving.set(true);
    this.error.set(null);
    this.memoryService.moveMemoryScope(this.memory().id, move).subscribe({
      next: memory => {
        this.saving.set(false);
        this.moved.emit(memory);
      },
      error: error => {
        this.saving.set(false);
        this.error.set(apiErrorMessage(error, 'Failed to move memory'));
      },
    });
  }

  private moveInput(): MemoryScopeMoveInput | null {
    if (this.isThreadMemory()) {
      const target = this.promoteTo();
      if (target === 'global') return { level: 'global' };
      const personalityId = this.personalityId();
      if (target === 'personality' && personalityId) return { level: 'personality', personality_id: personalityId };
      return null;
    }
    const chatId = this.threadId();
    return chatId ? { level: 'thread', chat_id: chatId } : null;
  }

  private loadSourceThread(chatId: string | undefined): Observable<Chat | null> {
    if (!chatId) return of(null);
    // An explicit id also finds an archived thread; a deleted one comes back empty.
    return this.chatService.listChatsPage(1, 1, { ids: chatId }).pipe(
      map(page => page.results?.find(chat => chat.id === chatId) ?? null),
      catchError(() => of(null)),
    );
  }

  private loadThreads(personalityId: string | null): Observable<Chat[]> {
    const filters = personalityId ? { personality_id: personalityId } : undefined;
    return this.chatService.listChatsPage(1, THREAD_PICKER_LIMIT, filters).pipe(
      map(page => page.results ?? []),
      catchError(() => of<Chat[]>([])),
    );
  }
}
