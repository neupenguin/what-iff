import { provideZonelessChangeDetection } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of, throwError } from 'rxjs';

import { Chat } from '../../../core/models/chat.model';
import { Memory } from '../../../core/models/memory.model';
import { ChatService } from '../../../core/services/chat.service';
import { MemoryService } from '../../../core/services/memory.service';
import { MemoryScopeDialogComponent } from './memory-scope-dialog.component';

function chat(id: string, partial: Partial<Chat> = {}): Chat {
  return {
    id,
    user_id: 'u-1',
    name: `Thread ${id}`,
    personality_id: 'persona-a',
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:00:00Z',
    ...partial,
  };
}

function memory(partial: Partial<Memory> = {}): Memory {
  return {
    id: 'm-1',
    content: 'Likes rain.',
    level: 'thread',
    type: 'Context',
    status: 'active',
    confidence: 0.6,
    starred: false,
    chat_id: 'chat-src',
    chat_name: 'Thread chat-src',
    created_at: '2026-08-28T00:00:00Z',
    updated_at: '2026-08-29T00:00:00Z',
    ...partial,
  };
}

describe('MemoryScopeDialogComponent', () => {
  let fixture: ComponentFixture<MemoryScopeDialogComponent>;
  let component: MemoryScopeDialogComponent;
  let listChatsPage: ReturnType<typeof vi.fn>;
  let moveMemoryScope: ReturnType<typeof vi.fn>;

  async function setup(mem: Memory, chats: { source?: Chat | null; threads?: Chat[] } = {}): Promise<void> {
    TestBed.resetTestingModule();
    listChatsPage = vi.fn((_page: number, _limit: number, filters?: { ids?: string }) => {
      const results = filters?.ids ? (chats.source ? [chats.source] : []) : (chats.threads ?? []);
      return of({ results, total_count: results.length, page: 1 });
    });
    moveMemoryScope = vi.fn().mockReturnValue(of(memory({ level: 'global' })));
    await TestBed.configureTestingModule({
      imports: [MemoryScopeDialogComponent],
      providers: [
        provideZonelessChangeDetection(),
        { provide: ChatService, useValue: { listChatsPage } },
        { provide: MemoryService, useValue: { moveMemoryScope } },
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(MemoryScopeDialogComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('memory', mem);
    fixture.componentRef.setInput('personalities', [
      { id: 'persona-a', label: 'Ada' },
      { id: 'persona-b', label: 'Bo' },
    ]);
    fixture.detectChanges();
  }

  describe('a thread memory', () => {
    it('offers global and its thread personality, and moves the record', async () => {
      await setup(memory(), { source: chat('chat-src') });
      const moved = vi.spyOn(component.moved, 'emit');

      expect(component.personalityName()).toBe('Ada');
      expect(component.canSubmit()).toBe(false);

      component.promoteTo.set('personality');
      component.submit();

      expect(moveMemoryScope).toHaveBeenCalledWith('m-1', { level: 'personality', personality_id: 'persona-a' });
      expect(moved).toHaveBeenCalled();
    });

    it('offers only global when its thread has no personality', async () => {
      await setup(memory(), { source: chat('chat-src', { personality_id: undefined }) });

      expect(fixture.nativeElement.querySelector('input[value="personality"]')).toBeNull();
      component.promoteTo.set('global');
      component.submit();
      expect(moveMemoryScope).toHaveBeenCalledWith('m-1', { level: 'global' });
    });

    it('does not list threads to pick from', async () => {
      await setup(memory(), { source: chat('chat-src') });
      expect(listChatsPage).toHaveBeenCalledTimes(1);
    });
  });

  describe('a promoted memory going back to a thread', () => {
    const promoted = memory({ level: 'global' });

    it('preselects the thread it came from, and lets the user pick another of its personality', async () => {
      await setup(promoted, { source: chat('chat-src'), threads: [chat('chat-src'), chat('chat-other')] });

      expect(listChatsPage).toHaveBeenLastCalledWith(1, 100, { personality_id: 'persona-a' });
      expect(component.threadId()).toBe('chat-src');
      expect(component.threadOptions().map(c => c.id)).toEqual(['chat-src', 'chat-other']);

      component.threadId.set('chat-other');
      component.submit();
      expect(moveMemoryScope).toHaveBeenCalledWith('m-1', { level: 'thread', chat_id: 'chat-other' });
    });

    it('preselects nothing when the source thread is gone', async () => {
      await setup(promoted, { source: null, threads: [chat('chat-other')] });

      expect(component.threadId()).toBeNull();
      expect(component.canSubmit()).toBe(false);
      expect(fixture.nativeElement.querySelector('[role="note"]')?.textContent).toContain('gone');
    });

    it('preselects nothing, and lists every thread, when no source thread is recorded', async () => {
      await setup(memory({ level: 'global', chat_id: undefined, chat_name: undefined }), { threads: [chat('chat-x')] });

      expect(listChatsPage).toHaveBeenCalledTimes(1);
      expect(listChatsPage).toHaveBeenCalledWith(1, 100, undefined);
      expect(component.threadId()).toBeNull();
      expect(component.canSubmit()).toBe(false);
    });

    it('stays within its pin: a source thread of another personality is not preselected', async () => {
      await setup(memory({ level: 'personality', pinned_personality_id: 'persona-b' }), {
        source: chat('chat-src'),
        threads: [chat('chat-b', { personality_id: 'persona-b' })],
      });

      expect(listChatsPage).toHaveBeenLastCalledWith(1, 100, { personality_id: 'persona-b' });
      expect(component.threadId()).toBeNull();
      expect(component.threadOptions().map(c => c.id)).toEqual(['chat-b']);
    });

    it('leaves out sandboxed threads other than the source', async () => {
      await setup(promoted, {
        source: chat('chat-src', { context_scope: 'sandbox' }),
        threads: [chat('chat-src', { context_scope: 'sandbox' }), chat('chat-sb', { context_scope: 'sandbox' })],
      });

      expect(component.threadOptions().map(c => c.id)).toEqual(['chat-src']);
    });

    it('shows the server error and stays open when the move is refused', async () => {
      await setup(promoted, { source: chat('chat-src'), threads: [chat('chat-src')] });
      moveMemoryScope.mockReturnValue(throwError(() => new Error('that thread belongs to a different personality')));
      const moved = vi.spyOn(component.moved, 'emit');

      component.submit();
      fixture.detectChanges();

      expect(moved).not.toHaveBeenCalled();
      expect(fixture.nativeElement.querySelector('[role="alert"]')?.textContent).toContain('different personality');
    });
  });
});
