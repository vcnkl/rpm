import React, { useEffect, useMemo, useReducer } from 'react';
import { Box, render, Text, useInput } from 'ink';
import { actionForKey, initialState, orderUnits, reduceEvent, type EnvState, type RuntimeEvent } from './state.js';
import fs from 'node:fs';
import process from 'node:process';

type StateAction =
  | { type: 'event'; event: RuntimeEvent }
  | { type: 'select'; delta: number }
  | { type: 'toggle_dependencies' };

function reducer(state: EnvState, action: StateAction): EnvState {
  if (action.type === 'event') return reduceEvent(state, action.event);
  if (action.type === 'toggle_dependencies') return { ...state, showDependencies: !state.showDependencies, selected: 0 };
  const visible = orderUnits(state.units, state.showDependencies);
  const next = Math.max(0, Math.min(visible.length - 1, state.selected + action.delta));
  return { ...state, selected: next };
}

function App() {
  const [state, dispatch] = useReducer(reducer, initialState(process.env.RPM_ENV_BLUEPRINT ?? 'environment'));
  const visible = useMemo(() => orderUnits(state.units, state.showDependencies), [state.units, state.showDependencies]);
  const selected = visible[state.selected];

  useEffect(() => {
    const eventStream = fs.createReadStream('/dev/fd/3', { encoding: 'utf8' });
    let buffer = '';
    const onData = (chunk: string) => {
      buffer += chunk;
      const lines = buffer.split('\n');
      buffer = lines.pop() ?? '';
      for (const line of lines) {
        if (!line.trim()) continue;
        try {
          dispatch({ type: 'event', event: JSON.parse(line) as RuntimeEvent });
        } catch {
          // Ignore malformed event lines so one bad write does not kill the TUI.
        }
      }
    };
    eventStream.on('data', onData);
    return () => {
      eventStream.off('data', onData);
      eventStream.destroy();
    };
  }, []);

  useInput((input, key) => {
    const action = actionForKey(input, key);
    if (action.type === 'select') dispatch(action);
    if (action.type === 'toggle_dependencies') dispatch(action);
    if (action.type === 'restart' && selected?.kind === 'target') send({ type: 'restart', ref: selected.ref });
    if (action.type === 'restart_all') send({ type: 'restart_all' });
    if (action.type === 'quit') send({ type: 'quit' });
  });

  return (
    <Box flexDirection="column">
      <Box>
        <Box width="35%" flexDirection="column" borderStyle="single" paddingX={1}>
          {visible.map((unit, index) => (
            <Text key={unit.ref} inverse={index === state.selected}>
              {unit.kind === 'dependency' ? 'dep' : 'target'} {unit.status} {unit.ref}
            </Text>
          ))}
        </Box>
        <Box width="65%" flexDirection="column" borderStyle="single" paddingX={1}>
          <Text>{selected ? `${selected.ref} ${selected.status}` : 'No runtime units yet'}</Text>
          {(selected?.output ?? []).slice(-20).map((line, index) => (
            <Text key={index}>{line}</Text>
          ))}
        </Box>
      </Box>
      <Box>
        <Text>
          {state.blueprint} reload:{' '}
          {state.units.some(unit => unit.status === 'reloading') ? 'active' : 'idle'} deps:{' '}
          {state.showDependencies ? 'shown' : 'hidden'}
        </Text>
      </Box>
    </Box>
  );
}

function send(action: { type: string; ref?: string }) {
  process.stdout.write(JSON.stringify(action) + '\n');
}

render(<App />, { stdout: process.stderr });
