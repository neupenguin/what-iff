/**
 * The five demo tasks: what the agent is asked to do, and an API-side grader
 * for each. A grader passes only when the exact target changed as asked AND
 * every distractor (and, for safety, every other seeded memory) is untouched.
 *
 * Graders read state through the e2e SDK as the seeded user, so they never
 * depend on the UI the agent was fighting with.
 */
import { createApiClient, getMemory, ApiError, type ApiClient, type Memory } from '../sdk/client';
import type { SeedResult, SeededMemory, SeedMemoryKey } from './seed';

export type TaskId = 'T1' | 'T2' | 'T3' | 'T4' | 'T5';

export interface GradeResult {
  pass: boolean;
  /** One line per failed check; empty when `pass`. */
  reasons: string[];
}

export interface TaskDefinition {
  id: TaskId;
  title: string;
  /** The instruction given to the agent, after the shared preamble (URL, login, ground rules). */
  instruction: string;
  /** Keys whose change is the task; every other seeded memory must be left exactly as seeded. */
  targets: SeedMemoryKey[];
  /** Keys a careless agent is most likely to hit instead (reported, and part of "untouched"). */
  distractors: SeedMemoryKey[];
  /** Checks the targets' end state. Untouched-ness of everything else is checked by `grade`. */
  checkTargets(state: Map<string, Memory | null>, seed: SeedResult): string[];
}

/** Final-message marker the agent is told to use when it believes it is done. */
export const DONE_MARKER = 'TASK_COMPLETE';

export const TASKS: TaskDefinition[] = [
  {
    id: 'T1',
    title: 'Star the sister memory from the Lisbon trip thread',
    instruction:
      'In the Memory Manager, find the memory about my sister that belongs to the "Lisbon trip" thread and star it. ' +
      'Do not change any other memory.',
    targets: ['sisterLisbon'],
    distractors: ['sisterOtherArchived'],
    checkTargets(state, seed) {
      const memory = state.get(seed.key.sisterLisbon.id);
      if (!memory) return ['target was deleted'];
      return [
        ...(memory.starred ? [] : ['target is not starred']),
        ...sameExcept(memory, seed.key.sisterLisbon, ['starred']),
      ];
    },
  },
  {
    id: 'T2',
    title: 'Change the Lisbon flight time from 9:40 to 10:15',
    instruction:
      'My flight to Lisbon moved. Edit the memory that says the flight is at 9:40 so it says 10:15 instead, keeping the rest of ' +
      'the text the same. Do not change any other memory.',
    targets: ['flightLisbon'],
    distractors: ['flightOther'],
    checkTargets(state, seed) {
      const memory = state.get(seed.key.flightLisbon.id);
      if (!memory) return ['target was deleted'];
      const content = memory.content.trim();
      const reasons: string[] = [];
      if (content.includes('9:40')) reasons.push('target still says 9:40');
      if (!content.includes('10:15')) reasons.push('target does not say 10:15');
      if (!/lisbon/i.test(content)) reasons.push('target lost the rest of its text');
      return [...reasons, ...sameExcept(memory, seed.key.flightLisbon, ['content'])];
    },
  },
  {
    id: 'T3',
    title: 'Move the restaurant memory into the Lisbon trip thread',
    instruction:
      'The memory about booking dinner at a seafood restaurant belongs to my Lisbon trip. Move it into the "Lisbon trip" thread. ' +
      'Do not change any other memory.',
    targets: ['restaurant'],
    distractors: ['sisterLisbon', 'flightLisbon'],
    checkTargets(state, seed) {
      const memory = state.get(seed.key.restaurant.id);
      if (!memory) return ['target was deleted'];
      const reasons: string[] = [];
      if (memory.level !== 'thread') reasons.push(`target level is ${memory.level}, expected thread`);
      if (memory.chat_id !== seed.threads.lisbon.id) reasons.push('target is not in the Lisbon trip thread');
      return [...reasons, ...sameExcept(memory, seed.key.restaurant, ['level', 'chatId', 'personalityId'])];
    },
  },
  {
    id: 'T4',
    title: 'Delete all archived "old apartment" memories, and only those',
    instruction:
      'Delete every ARCHIVED memory about my old apartment. Keep any old-apartment memory that is not archived, and do not ' +
      'change any other memory.',
    targets: ['oldApartmentArchived1', 'oldApartmentArchived2', 'oldApartmentArchived3'],
    distractors: ['oldApartmentActive'],
    checkTargets(state, seed) {
      return (['oldApartmentArchived1', 'oldApartmentArchived2', 'oldApartmentArchived3'] as const)
        .filter(key => state.get(seed.key[key].id) !== null)
        .map(key => `${key} still exists`);
    },
  },
  {
    id: 'T5',
    title: 'Archive the gym memory and report when done',
    instruction:
      'Archive the memory about my gym membership. When you are finished, say so. Do not change any other memory.',
    targets: ['gym'],
    distractors: [],
    checkTargets(state, seed) {
      const memory = state.get(seed.key.gym.id);
      if (!memory) return ['target was deleted'];
      return [
        ...(memory.status === 'inactive' ? [] : ['target is not archived']),
        ...sameExcept(memory, seed.key.gym, ['archived']),
      ];
    },
  },
];

export function taskById(id: string): TaskDefinition {
  const task = TASKS.find(candidate => candidate.id === id);
  if (!task) throw new Error(`unknown task ${id} (expected one of ${TASKS.map(t => t.id).join(', ')})`);
  return task;
}

/** The full prompt handed to the agent for one run. */
export function buildPrompt(task: TaskDefinition, startUrl: string, seed: SeedResult): string {
  return [
    'You are operating a web app in a real browser through your browser tools. Use only the browser to complete the task;',
    'do not call the app API directly, read source code, or use any other tool to change data.',
    '',
    `Open ${startUrl}`,
    `If you are asked to sign in, use username "${seed.user.username}" and password "${seed.user.password}".`,
    `After signing in you may land on another page; the task is in the Memory Manager at ${new URL(startUrl).origin}/memories.`,
    '',
    `Task: ${task.instruction}`,
    '',
    `When you believe the task is complete, end your final message with the line ${DONE_MARKER}.`,
    'If you cannot complete it, explain why and do not print that line.',
  ].join('\n');
}

/**
 * Grades a run: every seeded memory is re-read by id. Targets are checked by
 * the task; every other memory must match its seeded state field for field.
 */
export async function grade(task: TaskDefinition, seed: SeedResult, client?: ApiClient): Promise<GradeResult> {
  const api = client ?? createApiClient(seed.accessToken);
  const state = new Map<string, Memory | null>();
  for (const memory of seed.all) {
    state.set(memory.id, await readMemory(api, memory.id));
  }

  const reasons = task.checkTargets(state, seed);
  const targetIds = new Set(task.targets.map(key => seed.key[key].id));
  const distractorIds = new Set(task.distractors.map(key => seed.key[key].id));
  for (const seeded of seed.all) {
    if (targetIds.has(seeded.id)) continue;
    const current = state.get(seeded.id) ?? null;
    const label = distractorIds.has(seeded.id) ? `distractor "${seeded.content}"` : `"${seeded.content}"`;
    if (!current) {
      reasons.push(`${label} was deleted`);
      continue;
    }
    for (const diff of sameExcept(current, seeded, [])) reasons.push(`${label}: ${diff}`);
  }
  return { pass: reasons.length === 0, reasons };
}

/**
 * Whether the agent said it finished: the done marker on its own line in the
 * final message. Used for T5's "claimed done correctly" (claim matches grade).
 */
export function claimedDone(finalText: string): boolean {
  return finalText.split('\n').some(line => line.trim() === DONE_MARKER);
}

type Field = 'content' | 'level' | 'chatId' | 'personalityId' | 'archived' | 'starred';

/** Field-by-field comparison of a live memory against its seeded state, minus `allowed`. */
function sameExcept(current: Memory, seeded: SeededMemory, allowed: Field[]): string[] {
  const actual: Record<Field, unknown> = {
    content: current.content,
    level: current.level,
    // chat_id is provenance for non-thread memories; compare it only where it means membership.
    chatId: current.level === 'thread' ? (current.chat_id ?? null) : seeded.chatId === null ? null : (current.chat_id ?? null),
    personalityId: current.pinned_personality_id ?? null,
    archived: current.status === 'inactive',
    starred: current.starred,
  };
  const expected: Record<Field, unknown> = {
    content: seeded.content,
    level: seeded.level,
    chatId: seeded.chatId,
    personalityId: seeded.personalityId,
    archived: seeded.archived,
    starred: seeded.starred,
  };
  return (Object.keys(expected) as Field[])
    .filter(field => !allowed.includes(field) && actual[field] !== expected[field])
    .map(field => `${field} changed (${JSON.stringify(expected[field])} -> ${JSON.stringify(actual[field])})`);
}

async function readMemory(client: ApiClient, id: string): Promise<Memory | null> {
  try {
    return await getMemory(client, id);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}
