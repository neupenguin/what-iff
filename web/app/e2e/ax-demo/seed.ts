/**
 * Seeds one fresh account for an agent-experience demo run and prints what it
 * made as JSON (credentials, ids, and the distractor map the graders use).
 *
 *   npx tsx e2e/ax-demo/seed.ts > /tmp/seed.json
 *
 * Goes through the e2e SDK (`e2e/sdk/client.ts`) like every other suite here,
 * so `E2E_API_BASE_URL` retargets it. Nothing is deleted afterwards: these are
 * throwaway accounts on a local mock stack, and the runner seeds a new one per
 * run so no task can see another run's edits.
 */
import { resolve, sep } from 'node:path';

import {
  createApiClient,
  createChat,
  createMemoriesBatch,
  createPersonality,
  patchMemoriesBatch,
  type ApiClient,
  type MemoryCreateRequest,
} from '../sdk/client';
import { newTestUserDetails, registerUser, type TestUser } from '../fixtures/api';

/** Stable keys for the memories a task or grader refers to. */
export type SeedMemoryKey =
  | 'sisterLisbon'
  | 'sisterOtherArchived'
  | 'flightLisbon'
  | 'flightOther'
  | 'restaurant'
  | 'oldApartmentArchived1'
  | 'oldApartmentArchived2'
  | 'oldApartmentArchived3'
  | 'oldApartmentActive'
  | 'gym';

export interface SeededMemory {
  id: string;
  content: string;
  level: MemoryCreateRequest['level'];
  chatId: string | null;
  personalityId: string | null;
  archived: boolean;
  starred: boolean;
}

export interface SeedResult {
  user: TestUser;
  accessToken: string;
  personalities: Record<'planner' | 'coach' | 'chef', { id: string; name: string }>;
  threads: Record<'lisbon' | 'work' | 'home' | 'fitness', { id: string; name: string }>;
  /** The memories tasks name, by key. */
  key: Record<SeedMemoryKey, SeededMemory>;
  /** Every seeded memory (keyed ones included), as it was right after seeding. */
  all: SeededMemory[];
}

interface Spec {
  key?: SeedMemoryKey;
  content: string;
  thread?: keyof SeedResult['threads'];
  personality?: keyof SeedResult['personalities'];
  archived?: boolean;
  starred?: boolean;
}

/**
 * The memory set. Each task's target sits beside at least one distractor that
 * a selector-or-text-matching agent could plausibly hit instead: the same fact
 * in another thread, the same phrase archived and not, a near-identical time.
 * Contents are deliberately not uuid-tagged — the agent reads them on screen.
 */
function specs(): Spec[] {
  return [
    // T1: two sister memories, only one in Lisbon trip; the other is archived.
    { key: 'sisterLisbon', content: "My sister's name is Priya and she lives in Lisbon", thread: 'lisbon' },
    { key: 'sisterOtherArchived', content: "My sister's name is Priya", thread: 'home', archived: true },
    // T2: flight at 9:40 in Lisbon trip; a different flight in Work.
    { key: 'flightLisbon', content: 'Flight to Lisbon is at 9:40 on Friday', thread: 'lisbon' },
    { key: 'flightOther', content: 'Flight to Chicago for the offsite is at 7:15 on Monday', thread: 'work' },
    // T3: a restaurant memory that belongs in Lisbon trip but lives in Home.
    { key: 'restaurant', content: 'Book dinner at Cervejaria Ramiro, the seafood restaurant Priya recommended', thread: 'home' },
    // T4: three archived "old apartment" memories, plus one that is still active.
    { key: 'oldApartmentArchived1', content: 'Old apartment: the landlord was Mr. Okafor', personality: 'planner', archived: true },
    { key: 'oldApartmentArchived2', content: 'Old apartment had a broken dishwasher', thread: 'home', archived: true },
    { key: 'oldApartmentArchived3', content: 'Old apartment lease ended in March', archived: true },
    { key: 'oldApartmentActive', content: 'Still owed a deposit refund from the old apartment', thread: 'home' },
    // T5
    { key: 'gym', content: 'Gym membership renews on the 1st of each month', thread: 'fitness' },

    // Background so the list is long enough to need scrolling or searching.
    { content: 'Prefers aisle seats on flights', personality: 'planner' },
    { content: 'Lisbon hotel is in Alfama, check-in after 3pm', thread: 'lisbon' },
    { content: 'Wants to visit the Gulbenkian museum in Lisbon', thread: 'lisbon' },
    { content: 'Pack a travel adapter for Portugal', thread: 'lisbon' },
    { content: 'Learning a few Portuguese phrases before the trip', thread: 'lisbon', starred: true },
    { content: 'Quarterly planning review is on the 14th', thread: 'work' },
    { content: 'Manager is Dana; 1:1s are on Tuesdays', thread: 'work' },
    { content: 'Prefers async status updates over meetings', thread: 'work' },
    { content: 'Working on the accessibility audit project', thread: 'work', starred: true },
    { content: 'Expense reports are due by the 5th', thread: 'work' },
    { content: 'Partner is allergic to peanuts', personality: 'chef' },
    { content: 'Prefers vegetarian dinners on weekdays', personality: 'chef' },
    { content: 'Favorite cuisine is Thai', personality: 'chef' },
    { content: 'Has a cast-iron skillet and a rice cooker', personality: 'chef' },
    { content: 'Running a half marathon in November', thread: 'fitness' },
    { content: 'Knee gets sore after long runs; stretch first', personality: 'coach' },
    { content: 'Aiming for three strength sessions a week', personality: 'coach' },
    { content: 'Swims on Saturday mornings', thread: 'fitness' },
    { content: 'Dog is named Biscuit', thread: 'home' },
    { content: 'Plants need watering on Sundays', thread: 'home' },
    { content: 'Recycling goes out on Wednesday nights', thread: 'home' },
    { content: 'Wi-Fi router is in the hallway closet', thread: 'home', archived: true },
    { content: 'Lives in a two-bedroom apartment downtown' },
    { content: 'Birthday is in June' },
    { content: 'Prefers metric units' },
    { content: 'Uses a standing desk', starred: true },
    { content: 'Allergic to cats' },
    { content: 'Coffee order is a flat white' },
    { content: 'Time zone is Pacific', archived: true },
    { content: 'Reads mostly science fiction' },
  ];
}

const SYSTEM_PROMPT = 'You are a concise assistant used for an accessibility demo.';

export async function seed(): Promise<SeedResult> {
  const user = newTestUserDetails();
  const accessToken = await registerUser(user);
  const client = createApiClient(accessToken);

  const personalities = {
    planner: await persona(client, 'Trip Planner'),
    coach: await persona(client, 'Fitness Coach'),
    chef: await persona(client, 'Home Chef'),
  };
  const threads = {
    lisbon: await thread(client, 'Lisbon trip', personalities.planner.id),
    work: await thread(client, 'Work notes', personalities.planner.id),
    home: await thread(client, 'Home admin', personalities.chef.id),
    fitness: await thread(client, 'Training plan', personalities.coach.id),
  };

  const list = specs();
  const items: MemoryCreateRequest[] = list.map(spec => ({
    content: spec.content,
    level: spec.thread ? 'thread' : spec.personality ? 'personality' : 'global',
    chat_id: spec.thread ? threads[spec.thread].id : null,
    pinned_personality_id: spec.personality ? personalities[spec.personality].id : null,
    type: 'Context',
    starred: spec.starred ?? false,
  }));
  const created = (await createMemoriesBatch(client, items, true)).results ?? [];
  if (created.length !== items.length) {
    throw new Error(`seed: created ${created.length} of ${items.length} memories`);
  }

  // The batch endpoint returns results in request order; match on content anyway
  // so a reordering server cannot silently swap a target with its distractor.
  const byContent = new Map(created.map(memory => [memory.content, memory]));
  const all: SeededMemory[] = list.map(spec => {
    const memory = byContent.get(spec.content);
    if (!memory) throw new Error(`seed: no created memory for "${spec.content}"`);
    return {
      id: memory.id,
      content: memory.content,
      level: memory.level,
      chatId: spec.thread ? threads[spec.thread].id : null,
      personalityId: spec.personality ? personalities[spec.personality].id : null,
      archived: spec.archived ?? false,
      starred: spec.starred ?? false,
    };
  });

  const toArchive = all.filter(memory => memory.archived).map(memory => memory.id);
  if (toArchive.length > 0) {
    await patchMemoriesBatch(client, toArchive, { status: 'inactive' }, true);
  }

  const key = {} as Record<SeedMemoryKey, SeededMemory>;
  list.forEach((spec, index) => {
    if (spec.key) key[spec.key] = all[index];
  });

  return { user, accessToken, personalities, threads, key, all };
}

async function persona(client: ApiClient, name: string): Promise<{ id: string; name: string }> {
  const created = await createPersonality(client, { name, systemPrompt: SYSTEM_PROMPT });
  return { id: created.id as string, name };
}

async function thread(client: ApiClient, name: string, personalityId: string): Promise<{ id: string; name: string }> {
  const created = await createChat(client, { name, personalityId });
  if (!created.id) throw new Error(`seed: chat "${name}" came back without an id`);
  return { id: created.id, name };
}

// Run directly: print the seed as JSON on stdout.
if (process.argv[1] && resolve(process.argv[1]).endsWith(`${sep}ax-demo${sep}seed.ts`)) {
  seed()
    .then(result => process.stdout.write(`${JSON.stringify(result, null, 2)}\n`))
    .catch((err: unknown) => {
      console.error(err);
      process.exit(1);
    });
}
