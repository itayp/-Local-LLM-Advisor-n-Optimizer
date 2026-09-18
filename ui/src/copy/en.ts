// Every string the UI shows, in one place, from day one (i18n-ready,
// English only). Screens import from here; they do not carry prose of
// their own. Step 7 adds the glossary (one-line explainers for every
// technical term) next to this file.

export const en = {
  app: {
    title: 'Local LLM Advisor',
    tagline: 'Which AI models fit this computer, and how well they run.',
    advancedOn: 'Advanced',
    version: (v: string) => `version ${v}`,
    daemonUnreachable: 'The advisor is not running on this computer. Start it and reload this page.',
  },
  figure: {
    estimatedLabel: 'estimated',
    measuredLabel: 'measured on this computer',
    estimatedPrefix: '≈',
  },
  nav: {
    home: 'Home',
    computer: 'Your computer',
    ollama: 'Ollama',
    models: 'Models',
    recommend: 'Recommend',
    benchmarks: 'Benchmarks',
    watch: 'New models',
    settings: 'Settings',
  },
  screens: {
    home: {
      title: 'Home',
      placeholder:
        'Your computer in one sentence, the model you use today, and the single most useful thing to do next.',
      step: 'build plan step 8',
    },
    computer: {
      title: 'Your computer',
      placeholder:
        'What this computer has — graphics card, memory, processor — in plain words, with the technical details behind "Show details".',
      step: 'build plan step 2',
    },
    ollama: {
      title: 'Ollama',
      placeholder:
        'Whether Ollama is installed and running, one button that says what it will do, and whether it is using your graphics card.',
      step: 'build plan step 3',
    },
    models: {
      title: 'Models',
      placeholder:
        'The models installed on this computer, whether each one fits, and how fast it runs — estimated until measured.',
      step: 'build plan steps 3, 4 and 8',
    },
    recommend: {
      title: 'Recommend',
      placeholder:
        'What you want to use AI for, and up to three models that fit this computer, each with the reasons and what it costs to get.',
      step: 'build plan step 5',
    },
    benchmarks: {
      title: 'Benchmarks',
      placeholder:
        'Run a short test on a model, keep the history, and compare two runs side by side.',
      step: 'build plan step 6',
    },
    watch: {
      title: 'New models',
      placeholder:
        'What the advisor checked for you, when, what it found, and why it did or did not tell you.',
      step: 'build plan step 10',
    },
    settings: {
      title: 'Settings',
      placeholder:
        'Notifications, where models and data are kept, the version, and checking for updates.',
      step: 'build plan step 8',
      advancedLabel: 'Show advanced details',
      advancedHelp:
        'Off by default. When on, screens also show the technical columns — memory arithmetic, quantization names, tokens per second breakdowns.',
    },
  },
  placeholder: {
    comingIn: (step: string) => `This screen is filled in by ${step}.`,
  },
} as const
