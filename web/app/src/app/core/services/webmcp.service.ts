import { Injectable, inject } from '@angular/core';
import { firstValueFrom } from 'rxjs';
import { AuthService } from './auth.service';
import { ChatService } from './chat.service';
import { MemoryService } from './memory.service';
import type { Memory } from '../models/memory.model';

/**
 * WebMCP tools for the agent-experience evals (`?webmcp=1` or `?webmcp=soup`).
 *
 * Registers the Memory Manager's actions with `navigator.modelContext` so an agent can call them
 * instead of driving the page. Both modes call the same services the UI uses:
 *
 * - `1` (good): named for what they do, described, typed, with compact results.
 * - `soup`: the same operations named `action_1`…`action_8`, no descriptions, untyped `p1`/`p2`/`p3`
 *   parameters and raw API results. The tool-layer analogue of div soup.
 *
 * The mode is read once at boot and kept in sessionStorage, so the login redirect keeps it. It is
 * independent of the `?ax` level: every accessibility tier exposes identical tools.
 */
type Mode = 'good' | 'soup';
const QUERY_PARAM = 'webmcp';
const STORAGE_KEY = 'whatiff.webmcp';

interface ToolDef {
  name: string;
  description?: string;
  inputSchema: Record<string, unknown>;
  execute: (args: Record<string, unknown>) => Promise<unknown>;
}

interface ModelContext {
  registerTool(tool: ToolDef): unknown;
}

function readMode(): Mode | null {
  if (typeof window === 'undefined') return null;
  try {
    const raw = new URLSearchParams(window.location.search).get(QUERY_PARAM);
    if (raw !== null) {
      const mode = raw === 'soup' ? 'soup' : raw === '1' ? 'good' : null;
      if (mode) window.sessionStorage.setItem(STORAGE_KEY, mode);
      else window.sessionStorage.removeItem(STORAGE_KEY);
      return mode;
    }
    const stored = window.sessionStorage.getItem(STORAGE_KEY);
    return stored === 'good' || stored === 'soup' ? stored : null;
  } catch {
    return null;
  }
}

@Injectable({ providedIn: 'root' })
export class WebMcpService {
  private readonly auth = inject(AuthService);
  private readonly chats = inject(ChatService);
  private readonly memories = inject(MemoryService);
  readonly mode = readMode();

  constructor() {
    const context = (navigator as unknown as { modelContext?: ModelContext }).modelContext;
    if (!this.mode || !context) return;
    for (const tool of this.mode === 'good' ? this.goodTools() : this.soupTools()) context.registerTool(tool);
  }

  // The operations, shared by both modes.
  private signIn = (username: string, password: string) => this.auth.login({ username, password }).then(() => 'signed in');
  private async allMemories(): Promise<Memory[]> {
    const out: Memory[] = [];
    for (const status of ['active', 'inactive'] as const) {
      for (let page = 1; ; page++) {
        const res = await firstValueFrom(this.memories.getMemories(page, 100, { status }));
        out.push(...res.results);
        if (out.length >= res.total_count || res.results.length === 0) break;
      }
    }
    return out;
  }
  private threads = async () => (await firstValueFrom(this.chats.listAllChats())).chats;
  private patch = (id: string, payload: Parameters<MemoryService['patchMemory']>[1]) => firstValueFrom(this.memories.patchMemory(id, payload));
  private moveTo = (id: string, threadId: string | null) =>
    firstValueFrom(this.memories.moveMemoryScope(id, threadId ? { level: 'thread', chat_id: threadId } : { level: 'global' }));
  private remove = (id: string) => firstValueFrom(this.memories.deleteMemory(id)).then(() => 'deleted');

  private compact = (m: Memory) => ({
    id: m.id,
    content: m.content,
    scope: m.level,
    thread: m.chat_name ?? null,
    starred: m.starred,
    archived: m.status === 'inactive',
  });

  private goodTools(): ToolDef[] {
    const id = { type: 'string', description: 'The memory id, from list_memories.' };
    return [
      {
        name: 'sign_in',
        description: 'Sign in to WhatIff. Required before any other tool.',
        inputSchema: { type: 'object', properties: { username: { type: 'string' }, password: { type: 'string' } }, required: ['username', 'password'] },
        execute: a => this.signIn(String(a['username']), String(a['password'])),
      },
      {
        name: 'list_memories',
        description: 'List memories, active and archived, with the thread each belongs to. Optionally filter by text, thread name or archived state.',
        inputSchema: {
          type: 'object',
          properties: {
            query: { type: 'string', description: 'Case-insensitive text the memory content must contain.' },
            thread: { type: 'string', description: 'Thread name, e.g. "Lisbon trip". Omit for every scope.' },
            archived: { type: 'boolean', description: 'true for archived memories only, false for active only.' },
          },
        },
        execute: async a => {
          const q = typeof a['query'] === 'string' ? a['query'].toLowerCase() : null;
          const thread = typeof a['thread'] === 'string' ? a['thread'].toLowerCase() : null;
          return (await this.allMemories())
            .map(this.compact)
            .filter(m => (q === null || m.content.toLowerCase().includes(q)) && (thread === null || m.thread?.toLowerCase() === thread) && (typeof a['archived'] !== 'boolean' || m.archived === a['archived']));
        },
      },
      {
        name: 'list_threads',
        description: 'List chat threads with their ids, for moving memories into a thread.',
        inputSchema: { type: 'object', properties: {} },
        execute: async () => (await this.threads()).map(t => ({ id: t.id, name: t.name, personality: t.personality_name ?? null })),
      },
      {
        name: 'star_memory',
        description: 'Star or unstar one memory.',
        inputSchema: { type: 'object', properties: { memory_id: id, starred: { type: 'boolean' } }, required: ['memory_id', 'starred'] },
        execute: async a => this.compact(await this.patch(String(a['memory_id']), { starred: Boolean(a['starred']) })),
      },
      {
        name: 'edit_memory',
        description: "Replace one memory's text.",
        inputSchema: { type: 'object', properties: { memory_id: id, content: { type: 'string', description: 'The full new text.' } }, required: ['memory_id', 'content'] },
        execute: async a => this.compact(await this.patch(String(a['memory_id']), { content: String(a['content']) })),
      },
      {
        name: 'move_memory',
        description: 'Move one memory into a thread, or make it global.',
        inputSchema: {
          type: 'object',
          properties: { memory_id: id, thread_id: { type: ['string', 'null'], description: 'Destination thread id from list_threads, or null for global.' } },
          required: ['memory_id', 'thread_id'],
        },
        execute: async a => this.compact(await this.moveTo(String(a['memory_id']), (a['thread_id'] as string | null) ?? null)),
      },
      {
        name: 'archive_memory',
        description: 'Archive or unarchive one memory.',
        inputSchema: { type: 'object', properties: { memory_id: id, archived: { type: 'boolean' } }, required: ['memory_id', 'archived'] },
        execute: async a => this.compact(await this.patch(String(a['memory_id']), { status: a['archived'] ? 'inactive' : 'active' })),
      },
      {
        name: 'delete_memory',
        description: 'Delete one memory permanently.',
        inputSchema: { type: 'object', properties: { memory_id: id }, required: ['memory_id'] },
        execute: a => this.remove(String(a['memory_id'])),
      },
    ];
  }

  /** Same operations, stripped of every hint: generic names, no descriptions, untyped params, raw results. */
  private soupTools(): ToolDef[] {
    const params = (n: number) => ({ type: 'object', properties: Object.fromEntries(Array.from({ length: n }, (_, i) => [`p${i + 1}`, {}])) });
    return [
      { name: 'action_1', inputSchema: params(2), execute: async a => this.moveTo(String(a['p1']), (a['p2'] as string | null) ?? null) },
      { name: 'action_2', inputSchema: params(0), execute: () => this.allMemories() },
      { name: 'action_3', inputSchema: params(2), execute: a => this.patch(String(a['p1']), { starred: Boolean(a['p2']) }) },
      { name: 'action_4', inputSchema: params(2), execute: a => this.signIn(String(a['p1']), String(a['p2'])) },
      { name: 'action_5', inputSchema: params(1), execute: a => this.remove(String(a['p1'])) },
      { name: 'action_6', inputSchema: params(2), execute: a => this.patch(String(a['p1']), { content: String(a['p2']) }) },
      { name: 'action_7', inputSchema: params(0), execute: () => this.threads() },
      { name: 'action_8', inputSchema: params(2), execute: a => this.patch(String(a['p1']), { status: a['p2'] ? 'inactive' : 'active' }) },
    ];
  }
}
