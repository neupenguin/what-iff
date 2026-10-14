import { provideZonelessChangeDetection } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideHttpClient, withXhr } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { environment } from '@environments/environment';
import { Personality } from '../models/personality.model';
import { PersonalityChange, PersonalityService } from './personality.service';
import { makeTestPersonality } from '../models/personality.test-fixtures';

describe('PersonalityService', () => {
    let service: PersonalityService;
    let httpMock: HttpTestingController;

    beforeEach(() => {
        TestBed.configureTestingModule({
            providers: [
                provideZonelessChangeDetection(),
                provideHttpClient(withXhr()),
                provideHttpClientTesting(),
                PersonalityService,
            ],
        });

        service = TestBed.inject(PersonalityService);
        httpMock = TestBed.inject(HttpTestingController);
    });

    afterEach(() => {
        httpMock.verify();
    });

    it('serializes personality_ids filters as comma-separated personality_ids', () => {
        const personalities: Personality[] = [
            {
                id: 'personality-1',
                name: 'Vera',
                system_prompt: 'Be mysterious.',
                auto_pin_memories: false,
                expressions_enabled: true,
                image_style: 'auto', cover_image_id: null,
                cover_image_url: null,
                created_at: '2026-04-28T00:00:00Z',
                updated_at: '2026-04-28T00:00:00Z',
                stats: {
                    chat_count: 2,
                    last_used_at: '2026-04-28T12:00:00Z',
                },
            },
        ];

        service
            .listPersonalities(2, 25, {
            personality_ids: ['personality-1', 'personality-2'],
        })
            .subscribe((response) => {
            expect(response.results).toEqual(personalities);
        });

        const req = httpMock.expectOne(`${environment.apiUrl}/personality?page=2&limit=25&personality_ids=personality-1,personality-2`);
        expect(req.request.method).toBe('GET');
        req.flush({ results: personalities, total_count: 1, page: 2 });
    });

    describe('changes$ (#318)', () => {
        let changes: PersonalityChange[];
        const created = makeTestPersonality({ id: 'p-new', name: 'Imported Card' });

        beforeEach(() => {
            changes = [];
            service.changes$.subscribe(change => changes.push(change));
        });

        it('publishes a created change after createPersonality, without refetching a list itself', () => {
            service.createPersonality({ name: 'Imported Card', system_prompt: 'sp' }).subscribe();
            httpMock.expectOne(`${environment.apiUrl}/personality`).flush(created);

            expect(changes).toEqual([{ kind: 'created', personality: created }]);
            // Consumers refetch with their own page/filters; the service no longer fires a page-1 GET.
            httpMock.expectNone(req => req.method === 'GET');
        });

        it('publishes a created change after a character-card import', () => {
            service.importSillyTavernCard('{"spec":"chara_card_v3"}').subscribe();
            const req = httpMock.expectOne(`${environment.apiUrl}/personality/import/sillytavern`);
            expect(req.request.headers.get('Content-Type')).toBe('application/json');
            req.flush({ personality: created, warnings: [], lore_files: [] });

            expect(changes).toEqual([{ kind: 'created', personality: created }]);
        });

        it('publishes updated and deleted changes', () => {
            service.updatePersonality('p-new', { name: 'Renamed' } as any).subscribe();
            httpMock.expectOne(`${environment.apiUrl}/personality/p-new`).flush({ ...created, name: 'Renamed' });
            service.deletePersonality('p-new').subscribe();
            httpMock.expectOne(`${environment.apiUrl}/personality/p-new`).flush(null);

            expect(changes).toEqual([
                { kind: 'updated', personality: { ...created, name: 'Renamed' } },
                { kind: 'deleted', id: 'p-new' },
            ]);
        });

        it('publishes nothing when the mutation fails', () => {
            service.createPersonality({ name: 'x', system_prompt: 'y' }).subscribe({ error: () => undefined });
            httpMock.expectOne(`${environment.apiUrl}/personality`).flush('no', { status: 400, statusText: 'Bad Request' });

            expect(changes).toEqual([]);
        });

        it('invalidate() publishes an invalidated change', () => {
            service.invalidate();
            expect(changes).toEqual([{ kind: 'invalidated' }]);
        });
    });

    it('lists personality expressions', () => {
        service.listExpressions('personality-1').subscribe((expressions) => {
            expect(expressions).toEqual([
                {
                    expression_key: 'happy',
                    label: 'Happy',
                    image_id: 'image-1',
                    image_url: '/api/image-gallery/image-1?size=full',
                    created_at: '2026-04-28T00:00:00Z',
                    updated_at: '2026-04-28T00:00:00Z',
                },
            ]);
        });

        const req = httpMock.expectOne(`${environment.apiUrl}/personality/personality-1/expressions`);
        expect(req.request.method).toBe('GET');
        req.flush([
            {
                expression_key: 'happy',
                label: 'Happy',
                image_id: 'image-1',
                image_url: '/api/image-gallery/image-1?size=full',
                created_at: '2026-04-28T00:00:00Z',
                updated_at: '2026-04-28T00:00:00Z',
            },
        ]);
    });

    it('upserts personality expressions', () => {
        service
            .upsertExpression('personality-1', 'happy', {
            image_id: 'image-1',
            label: 'Happy',
        })
            .subscribe();

        const req = httpMock.expectOne(`${environment.apiUrl}/personality/personality-1/expressions/happy`);
        expect(req.request.method).toBe('PUT');
        expect(req.request.body).toEqual({
            image_id: 'image-1',
            label: 'Happy',
        });
        req.flush({
            expression_key: 'happy',
            label: 'Happy',
            image_id: 'image-1',
            image_url: '/api/image-gallery/image-1?size=full',
            created_at: '2026-04-28T00:00:00Z',
            updated_at: '2026-04-28T00:00:00Z',
        });
    });

    it('deletes personality expressions', () => {
        service.deleteExpression('personality-1', 'happy').subscribe();

        const req = httpMock.expectOne(`${environment.apiUrl}/personality/personality-1/expressions/happy`);
        expect(req.request.method).toBe('DELETE');
        req.flush(null);
    });
});
