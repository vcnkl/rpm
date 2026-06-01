export type RuntimeEvent = {
  type: string;
  ref?: string;
  stream?: string;
  line?: string;
  error?: string;
};

export type UnitState = {
  ref: string;
  kind: 'target' | 'dependency' | 'environment';
  status: 'starting' | 'running' | 'reloading' | 'exited' | 'failed' | 'stopped';
  output: string[];
};

export type EnvState = {
  blueprint: string;
  selected: number;
  showDependencies: boolean;
  units: UnitState[];
};

const maxLines = 200;

export function initialState(blueprint = 'environment'): EnvState {
  return { blueprint, selected: 0, showDependencies: true, units: [] };
}

export function reduceEvent(state: EnvState, event: RuntimeEvent): EnvState {
  switch (event.type) {
    case 'process_started':
      return upsertUnit(state, event.ref, 'target', 'running');
    case 'process_output':
      return appendOutput(upsertUnit(state, event.ref, 'target', 'running'), event.ref, event.line ?? '');
    case 'process_exited':
      return upsertUnit(state, event.ref, 'target', event.error ? 'failed' : 'exited');
    case 'dependency_started':
      return upsertUnit(state, event.ref, 'dependency', 'running');
    case 'dependency_failed':
      return upsertUnit(state, event.ref ?? 'dependencies', 'dependency', 'failed');
    case 'reload_started':
      return upsertUnit(state, event.ref, 'target', 'reloading');
    case 'reload_completed':
      return upsertUnit(state, event.ref, 'target', event.error ? 'failed' : 'running');
    case 'environment_stopped':
      return { ...state, units: state.units.map(unit => ({ ...unit, status: unit.status === 'failed' ? 'failed' : 'stopped' })) };
    default:
      return state;
  }
}

export function orderUnits(units: UnitState[], showDependencies = true): UnitState[] {
  return units
    .filter(unit => showDependencies || unit.kind !== 'dependency')
    .slice()
    .sort((a, b) => {
      if (a.kind !== b.kind) return a.kind === 'dependency' ? -1 : 1;
      return a.ref.localeCompare(b.ref);
    });
}

export type KeyAction = { type: 'select'; delta: number } | { type: 'restart' } | { type: 'restart_all' } | { type: 'toggle_dependencies' } | { type: 'quit' } | { type: 'none' };

export function actionForKey(input: string, key: { upArrow?: boolean; downArrow?: boolean }): KeyAction {
  if (key.upArrow || input === 'k') return { type: 'select', delta: -1 };
  if (key.downArrow || input === 'j') return { type: 'select', delta: 1 };
  if (input === 'r') return { type: 'restart' };
  if (input === 'R') return { type: 'restart_all' };
  if (input === 'd') return { type: 'toggle_dependencies' };
  if (input === 'q') return { type: 'quit' };
  return { type: 'none' };
}

function upsertUnit(state: EnvState, ref = 'environment', kind: UnitState['kind'], status: UnitState['status']): EnvState {
  const units = state.units.slice();
  const index = units.findIndex(unit => unit.ref === ref);
  if (index >= 0) {
    units[index] = { ...units[index], kind, status };
  } else {
    units.push({ ref, kind, status, output: [] });
  }
  return { ...state, units };
}

function appendOutput(state: EnvState, ref = 'environment', line: string): EnvState {
  return {
    ...state,
    units: state.units.map(unit => {
      if (unit.ref !== ref) return unit;
      return { ...unit, output: [...unit.output, line].slice(-maxLines) };
    }),
  };
}
