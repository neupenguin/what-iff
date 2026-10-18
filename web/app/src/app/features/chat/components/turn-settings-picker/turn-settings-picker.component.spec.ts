import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideZonelessChangeDetection } from '@angular/core';

import { TurnSettingsPickerComponent } from './turn-settings-picker.component';

describe('TurnSettingsPickerComponent', () => {
    let fixture: ComponentFixture<TurnSettingsPickerComponent>;
    let host: HTMLElement;

    beforeEach(async () => {
        await TestBed.configureTestingModule({
            imports: [TurnSettingsPickerComponent],
            providers: [provideZonelessChangeDetection()],
        }).compileComponents();

        fixture = TestBed.createComponent(TurnSettingsPickerComponent);
        host = fixture.nativeElement as HTMLElement;
    });

    function trigger(): HTMLButtonElement {
        return host.querySelector('.turn-settings__trigger') as HTMLButtonElement;
    }

    function open(): void {
        trigger().click();
        fixture.detectChanges();
    }

    it('shows the thread effort and loop, and emits the thread changes', () => {
        fixture.componentRef.setInput('reasoningSupport', 'optional');
        fixture.componentRef.setInput('threadReasoningEffort', 'high');
        fixture.componentRef.setInput('threadAgentLoop', 'advanced');
        fixture.detectChanges();
        expect(trigger().textContent).toContain('High · Advanced');
        expect(trigger().textContent).not.toContain('set by');

        const efforts: unknown[] = [];
        const loops: unknown[] = [];
        fixture.componentInstance.reasoningEffortChange.subscribe(value => efforts.push(value));
        fixture.componentInstance.agentLoopChange.subscribe(value => loops.push(value));
        open();

        const radios = [...host.querySelectorAll<HTMLInputElement>('input[type=radio]')];
        const labels = radios.map(radio => {
            const label = radio.closest('label')!;
            return (label.querySelector('.turn-settings__option-label') ?? label).textContent?.trim();
        });
        expect(labels).toEqual(['Model default', 'Off', 'Low', 'Medium', 'High', 'Standard', 'Advanced']);
        radios[0].click();
        radios[5].click();
        expect(efforts).toEqual([null]);
        expect(loops).toEqual(['standard']);
    });

    it('hides the reasoning part when the model has no effort control', () => {
        fixture.componentRef.setInput('reasoningSupport', 'none');
        fixture.componentRef.setInput('threadReasoningEffort', 'high');
        fixture.detectChanges();
        expect(trigger().textContent).toContain('Standard');
        open();
        expect(host.querySelectorAll('legend').length).toBe(1);
        expect(host.querySelector('legend')?.textContent).toContain('Agent loop');
    });

    it('offers Off / On for a toggle model and checks On for any level', () => {
        fixture.componentRef.setInput('reasoningSupport', 'toggle');
        fixture.componentRef.setInput('threadReasoningEffort', 'low');
        fixture.detectChanges();
        expect(trigger().textContent).toContain('On');
        open();
        const checked = host.querySelector<HTMLInputElement>('input[type=radio]:checked');
        expect(checked?.closest('label')?.textContent?.trim()).toBe('On');
    });

    it('shows the mode value as effective and says the mode set it', () => {
        fixture.componentRef.setInput('reasoningSupport', 'optional');
        fixture.componentRef.setInput('threadReasoningEffort', 'low');
        fixture.componentRef.setInput('activeMode', { name: 'Deep work', reasoning_effort: 'high', agent_loop: null });
        fixture.detectChanges();

        expect(trigger().textContent).toContain('High');
        expect(trigger().textContent).toContain('set by mode');
        expect(trigger().getAttribute('aria-label')).toContain('set by the Deep work mode');

        open();
        expect(host.querySelector('.turn-settings__mode-note')?.textContent).toContain('Deep work mode sets High');
        // The popover still edits the thread's own value.
        const checked = host.querySelector<HTMLInputElement>('input[type=radio]:checked');
        expect(checked?.closest('label')?.textContent?.trim()).toBe('Low');
    });
});
