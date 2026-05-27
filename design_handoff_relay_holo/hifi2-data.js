// Sample data for Relay sci-fi hi-fi mocks (Job Detail anchor screen).
window.RelayJob = (function () {
  const JOB = {
    id: '9F4E1C',
    name: 'film-x / shot-042 render',
    status: 'running',
    pct: 72,
    started: '2026-05-07T14:22:08Z',
    elapsed: '14m 02s',
    eta: '5m 24s',
    duration: '14m',
    owner: 'mira@studio.dev',
    priority: 'high',
    source: 'cli',
    image: 'relay/blender:4.2-cuda',
    runtime: 'cuda · 24 GB · 32 vCPU',
    parallelism: 16,
    cluster: 'farm-west / pod-b',
    cmd: 'relay run blender --frames 1-1000 --denoise --out s3://renders/',
    tasksDone: 48,
    tasksTotal: 64,
    cpu: 78, mem: 64, gpu: 91, net: 42,
  };

  // Tasks: [id, idx, status, pct, worker, dur]
  const TASKS = [
    ['t-041', 41, 'done',     100, 'farm-west-12', '13s'],
    ['t-042', 42, 'done',     100, 'farm-west-09', '14s'],
    ['t-043', 43, 'done',     100, 'farm-west-12', '13s'],
    ['t-044', 44, 'done',     100, 'farm-west-04', '15s'],
    ['t-045', 45, 'done',     100, 'farm-west-12', '12s'],
    ['t-046', 46, 'done',     100, 'farm-west-09', '14s'],
    ['t-047', 47, 'done',     100, 'farm-west-04', '13s'],
    ['t-048', 48, 'done',     100, 'farm-west-12', '14s'],
    ['t-049', 49, 'running',   62, 'farm-west-04', '8s'],
    ['t-050', 50, 'running',   41, 'farm-west-09', '5s'],
    ['t-051', 51, 'running',   28, 'farm-west-12', '3s'],
    ['t-052', 52, 'running',   18, 'farm-west-04', '2s'],
    ['t-053', 53, 'queued',     0, '—', '—'],
    ['t-054', 54, 'queued',     0, '—', '—'],
    ['t-055', 55, 'queued',     0, '—', '—'],
    ['t-056', 56, 'queued',     0, '—', '—'],
  ];

  // DAG nodes: [id, label, status, x, y]
  const DAG = {
    nodes: [
      ['n0', 'fetch.assets',   'done',     1, 1],
      ['n1', 'unpack',         'done',     2, 1],
      ['n2', 'preflight',      'done',     3, 1],
      ['n3', 'render.frames',  'running',  4, 1],
      ['n4', 'denoise',        'queued',   5, 1],
      ['n5', 'encode.proxy',   'queued',   5, 0],
      ['n6', 'upload.s3',      'queued',   6, 1],
      ['n7', 'notify',         'queued',   7, 1],
    ],
    edges: [
      ['n0','n1'],['n1','n2'],['n2','n3'],
      ['n3','n4'],['n3','n5'],
      ['n4','n6'],['n5','n6'],['n6','n7'],
    ],
  };

  const LOG = [
    ['14:36:02','INFO ','task','t-048 complete · 14s · farm-west-12'],
    ['14:36:05','INFO ','task','t-049 dispatch → farm-west-04'],
    ['14:36:05','INFO ','task','t-050 dispatch → farm-west-09'],
    ['14:36:08','INFO ','task','t-051 dispatch → farm-west-12'],
    ['14:36:11','DEBUG','gpu ','farm-west-04 · cuda 0 · util 91% · vram 18.2/24 GB'],
    ['14:36:14','INFO ','task','t-052 dispatch → farm-west-04'],
    ['14:36:18','WARN ','net ','retry 1/3 · s3://renders/ · 503'],
    ['14:36:19','INFO ','net ','recovered · 217ms'],
    ['14:36:22','INFO ','task','t-049 progress 62%'],
    ['14:36:24','INFO ','task','t-050 progress 41%'],
  ];

  // 60-frame sparkline (fake but plausible GPU util)
  const SPARK = [42,55,68,72,80,84,88,90,91,89,86,82,78,74,72,75,80,86,90,93,94,93,90,86,82,79,76,74,72,71,72,75,79,83,87,90,92,93,93,92,90,88,86,84,82,80,79,78,77,77,78,80,83,86,89,91,92,92,91,90];

  return { JOB, TASKS, DAG, LOG, SPARK };
})();
