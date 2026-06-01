import assert from 'node:assert/strict';
import test from 'node:test';
import { actionForKey, initialState, orderUnits, reduceEvent } from './state.ts';

test('reduces runtime events into bounded unit state', () => {
  let state = initialState('local');
  state = reduceEvent(state, { type: 'process_started', ref: 'api:serve' });
  state = reduceEvent(state, { type: 'process_output', ref: 'api:serve', line: 'hello' });
  state = reduceEvent(state, { type: 'process_exited', ref: 'api:serve' });

  assert.equal(state.units[0].status, 'exited');
  assert.deepEqual(state.units[0].output, ['hello']);
});

test('orders dependencies before targets and hides dependencies when toggled', () => {
  const units = [
    { ref: 'web:serve', kind: 'target' as const, status: 'running' as const, output: [] },
    { ref: 'api:postgres', kind: 'dependency' as const, status: 'running' as const, output: [] },
    { ref: 'api:serve', kind: 'target' as const, status: 'running' as const, output: [] },
  ];

  assert.deepEqual(orderUnits(units).map(unit => unit.ref), ['api:postgres', 'api:serve', 'web:serve']);
  assert.deepEqual(orderUnits(units, false).map(unit => unit.ref), ['api:serve', 'web:serve']);
});

test('maps keyboard input to TUI actions', () => {
  assert.deepEqual(actionForKey('j', {}), { type: 'select', delta: 1 });
  assert.deepEqual(actionForKey('', { upArrow: true }), { type: 'select', delta: -1 });
  assert.deepEqual(actionForKey('r', {}), { type: 'restart' });
  assert.deepEqual(actionForKey('R', {}), { type: 'restart_all' });
  assert.deepEqual(actionForKey('d', {}), { type: 'toggle_dependencies' });
  assert.deepEqual(actionForKey('q', {}), { type: 'quit' });
});
