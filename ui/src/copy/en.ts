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
      loading: 'Looking at this computer…',
      failed: (message: string) => `This computer could not be read: ${message}`,
      changed:
        "This computer's hardware has changed since the advisor last started. Test results from before stay with the old hardware.",
      factsLabel: 'What this computer has',
      graphics: 'Graphics',
      memory: 'Memory',
      processor: 'Processor',
      modelsSpace: 'Space for models',
      system: 'System',
      noGraphics: 'No graphics card',
      unknown: 'could not be read',
      gpuMemory: (size: string) => `${size} of graphics memory`,
      gpuMemoryUnknown: 'its memory could not be read',
      builtIn: 'built into the processor',
      notUsable: 'Ollama cannot use it',
      unifiedShare: (total: string, usable: string) => `${total}, of which the graphics can use ${usable}`,
      unifiedShareUnknown: (total: string) => `${total}; how much of it the graphics can use could not be read`,
      cores: (physical: number, logical: number) =>
        physical > 0 && logical > physical
          ? `${physical} cores, ${logical} threads`
          : physical > 0
            ? `${physical} cores`
            : logical > 0
              ? `${logical} threads`
              : '',
      freeSpace: (free: string) => `${free} free`,
      folderNotYet: 'Ollama has not created its models folder yet; this is the free space on the drive it will use.',
      showDetails: 'Show details',
      problemsTitle: 'What could not be read',
      filteredTitle: 'Display devices that cannot run models',
      advanced: {
        title: 'Technical details',
        device: 'Device',
        path: 'Runtime path Ollama should use',
        why: 'Why',
        driver: 'Driver',
        memorySource: 'Memory read from',
        ids: 'Identifiers',
        vector: 'Processor extensions',
        vectorValue: (avx2: boolean, avx512: boolean) => `AVX2 ${avx2 ? 'yes' : 'no'}, AVX-512 ${avx512 ? 'yes' : 'no'}`,
        vectorArm: 'ARM processor (AVX does not apply)',
        budget: 'Graphics memory budget',
        modelsFolder: 'Models folder',
        expectations: 'Expectations checked against',
        profile: (id: number, fingerprint: string) => `profile ${id} · ${fingerprint}`,
      },
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
