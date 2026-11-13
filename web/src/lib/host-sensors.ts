// The managed computer's sensor readings as the server hands them on: the
// kinds, their units and how the dashboard orders and prints them.

export type HostSensor = { hw: string; type: string; name: string; kind: string; value: number };

// Kind: unit and decimals, in the order the dashboard lists kinds. The same
// kinds as the server's hostsensors package; the agent converts to the unit.
const kinds: Record<string, [unit: string, decimals: number]> = {
  temperature: ['°C', 1],
  fan: ['RPM', 0],
  control: ['%', 0],
  voltage: ['V', 3],
  current: ['A', 2],
  power: ['W', 1],
  load: ['%', 1],
  level: ['%', 1],
  clock: ['MHz', 0],
  frequency: ['Hz', 0],
  data: ['GB', 1],
  smalldata: ['MB', 0],
  throughput: ['B/s', 0],
  energy: ['mWh', 0],
  humidity: ['%', 0],
  flow: ['L/h', 0],
  noise: ['dBA', 0],
  factor: ['', 2]
};
const kindOrder = Object.keys(kinds);

// What the dashboard shows before "all readings" is asked for.
export const mainKinds = new Set(['temperature', 'fan', 'voltage', 'power']);

const typeOrder = ['cpu', 'motherboard', 'gpu', 'memory', 'storage', 'cooler', 'psu', 'battery'];

export function sensorValue(sensor: HostSensor): string {
  const [unit, decimals] = kinds[sensor.kind] ?? ['', 1];
  const text = sensor.value.toFixed(decimals);
  return unit ? `${text} ${unit}` : text;
}

export type HostSensorGroup = { hardware: string; type: string; sensors: HostSensor[] };

// Readings by the part they belong to: processor and board first, each
// part's readings by kind and otherwise in the agent's order.
export function groupSensors(sensors: HostSensor[]): HostSensorGroup[] {
  const groups = new Map<string, HostSensorGroup>();
  for (const sensor of sensors) {
    const key = `${sensor.type}\u0000${sensor.hw}`;
    let group = groups.get(key);
    if (!group) {
      group = { hardware: sensor.hw || sensor.type, type: sensor.type, sensors: [] };
      groups.set(key, group);
    }
    group.sensors.push(sensor);
  }
  const rank = (list: string[], value: string) => {
    const index = list.indexOf(value);
    return index < 0 ? list.length : index;
  };
  const result = [...groups.values()];
  for (const group of result)
    group.sensors = group.sensors
      .map((sensor, index) => ({ sensor, index }))
      .sort(
        (a, b) =>
          rank(kindOrder, a.sensor.kind) - rank(kindOrder, b.sensor.kind) || a.index - b.index
      )
      .map((item) => item.sensor);
  return result
    .map((group, index) => ({ group, index }))
    .sort(
      (a, b) => rank(typeOrder, a.group.type) - rank(typeOrder, b.group.type) || a.index - b.index
    )
    .map((item) => item.group);
}

// A reading's name across reports: the server's hostsensors.Key.
export const sensorKey = (sensor: HostSensor) =>
  [sensor.type, sensor.hw, sensor.kind, sensor.name].join('\t');

// Shown: the administrator's choice for this reading, or a main kind.
export const isShown = (sensor: HostSensor, overrides: Record<string, boolean>) =>
  overrides[sensorKey(sensor)] ?? mainKinds.has(sensor.kind);

// The exceptions to store after a choice: the readings of this report whose
// choice differs from the default, and the earlier exceptions of readings
// the report does not have now (a drive that is unplugged keeps its choice).
export function viewOverrides(
  sensors: HostSensor[],
  choice: Record<string, boolean>,
  previous: Record<string, boolean>
): Record<string, boolean> {
  const present = new Set(sensors.map(sensorKey));
  const result = Object.fromEntries(Object.entries(previous).filter(([key]) => !present.has(key)));
  for (const sensor of sensors) {
    const key = sensorKey(sensor);
    const shown = choice[key] ?? mainKinds.has(sensor.kind);
    if (shown !== mainKinds.has(sensor.kind)) result[key] = shown;
  }
  return result;
}
