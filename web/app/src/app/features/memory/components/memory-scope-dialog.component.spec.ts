import { provideZonelessChangeDetection } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of, throwError } from 'rxjs';

import { Chat } from '../../../core/models/chat.model';
import { Memory } from '../../../core/models/memory.model';
import { ChatService } from '../../../core/services/chat.service';
import { MemoryViewService } from '../../../core/services/memory-view.service';
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
    level: 'global',
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
  let moveScope: ReturnType<typeof vi.fn>;

  /** threads: the picker listing; byID: what an explicit-id lookup finds (e.g. an archived source). */
  async function setup(memories: Memory[], chats: { threads?: Chat[]; byID?: Chat[] } = {}): Promise<void> {
    TestBed.resetTestingModule();
    listChatsPage = vi.fn((_page: number, _limit: number, filters?: { ids?: string }) => {
      const results = filters?.ids ? (chats.byID ?? []).filter(c => c.id === filters.ids) : (chats.threads ?? []);
      return of({ results, total_count: results.length, page: 1 });
    });
    moveScope = vi.fn().mockReturnValue(of([]));
    await TestBed.configureTestingModule({
      imports: [MemoryScopeDialogComponent],
      providers: [
        provideZonelessChangeDetection(),
        { provide: ChatService, useValue: { listChatsPage } },
        { provide: MemoryViewService, useValue: { moveScope } },
      ],
    }).compileComponents();
    fixture = TestBed.createComponent(MemoryScopeDialogComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('memories', memories);
    fixture.detectChanges();
  }

  it('lists threads of every personality, unfiltered', async () => {
    await setup([memory({ chat_id: undefined })], {
      threads: [chat('chat-a'), chat('chat-b', { personality_id: 'persona-b', personality_name: 'Bo' })],
    });

    expect(listChatsPage).toHaveBeenCalledWith(1, 100);
    expect(component.threadOptions().map(c => c.id)).toEqual(['chat-a', 'chat-b']);
    expect(component.threadId()).toBeNull();
  });

  it('preselects and lists first the thread a single memory came from', async () => {
    await setup([memory()], { threads: [chat('chat-a'), chat('chat-src')] });

    expect(component.threadOptions().map(c => c.id)).toEqual(['chat-src', 'chat-a']);
    expect(component.threadId()).toBe('chat-src');
    expect(fixture.nativeElement.textContent).toContain('It came from “Thread chat-src”');
  });

  it('finds an archived source thread by id when the listing does not include it', async () => {
    await setup([memory()], { threads: [chat('chat-a')], byID: [chat('chat-src', { archived: true } as Partial<Chat>)] });

    expect(listChatsPage).toHaveBeenCalledWith(1, 1, { ids: 'chat-src' });
    expect(component.threadId()).toBe('chat-src');
  });

  it('does not offer a thread memory its own thread', async () => {
    await setup([memory({ level: 'thread', chat_id: 'chat-src' })], { threads: [chat('chat-src'), chat('chat-a')] });

    expect(component.threadOptions().map(c => c.id)).toEqual(['chat-a']);
    expect(component.threadId()).toBeNull();
  });

  it('offers a sandboxed thread only to a single memory that came from it', async () => {
    const sandbox = (id: string) => chat(id, { context_scope: 'sandbox' });
    await setup([memory({ chat_id: 'sb-src' })], { threads: [sandbox('sb-src'), sandbox('sb-other'), chat('chat-a')] });
    expect(component.threadOptions().map(c => c.id)).toEqual(['sb-src', 'chat-a']);

    await setup([memory({ id: 'm-1', chat_id: 'sb-src' }), memory({ id: 'm-2' })], { threads: [sandbox('sb-src'), chat('chat-a')] });
    expect(component.threadOptions().map(c => c.id)).toEqual(['chat-a']);
  });

  it('moves every memory to the chosen thread', async () => {
    await setup([memory({ id: 'm-1' }), memory({ id: 'm-2', level: 'thread' })], { threads: [chat('chat-a')] });
    const moved = vi.spyOn(component.moved, 'emit');

    expect(component.title()).toBe('Move 2 memories to a thread');
    expect(component.canSubmit()).toBe(false);
    component.threadId.set('chat-a');
    component.submit();

    expect(moveScope).toHaveBeenCalledWith(['m-1', 'm-2'], { level: 'thread', chat_id: 'chat-a' });
    expect(moved).toHaveBeenCalled();
  });

  it('shows refusals and reports the moves that went through on close', async () => {
    await setup([memory({ id: 'm-1' }), memory({ id: 'm-2' })], { threads: [chat('chat-a')] });
    moveScope.mockReturnValue(of(['a sandboxed thread only takes back memories made in it']));
    const moved = vi.spyOn(component.moved, 'emit');
    const cancel = vi.spyOn(component.cancel, 'emit');

    component.threadId.set('chat-a');
    component.submit();
    fixture.detectChanges();

    expect(moved).not.toHaveBeenCalled();
    expect(fixture.nativeElement.querySelector('[role="alert"]')?.textContent).toContain('Some memories could not be moved');
    expect(component.canSubmit()).toBe(false);

    component.close();
    expect(moved).toHaveBeenCalled();
    expect(cancel).not.toHaveBeenCalled();
  });

  it('cancels when nothing moved', async () => {
    await setup([memory()], { threads: [chat('chat-a')] });
    moveScope.mockReturnValue(of(['nope']));
    const cancel = vi.spyOn(component.cancel, 'emit');

    component.threadId.set('chat-a');
    component.submit();
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('[role="alert"]')?.textContent).toContain('Nothing was moved: nope');

    component.close();
    expect(cancel).toHaveBeenCalled();
  });

  it('recovers from a failure outside the per-memory requests', async () => {
    await setup([memory()], { threads: [chat('chat-a')] });
    moveScope.mockReturnValue(throwError(() => new Error('boom')));

    component.threadId.set('chat-a');
    component.submit();
    fixture.detectChanges();

    expect(component.saving()).toBe(false);
    expect(fixture.nativeElement.querySelector('[role="alert"]')?.textContent).toContain('Failed to move memories');
  });
});
