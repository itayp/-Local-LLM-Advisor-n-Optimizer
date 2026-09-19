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
      question: 'What do you want to use AI for?',
      questionHelp: 'Pick one or more. The advisor suggests up to three models that fit this computer.',
      purposes: {
        chat: 'Everyday questions and chat',
        writing: 'Writing and editing text',
        coding: 'Writing and fixing code',
        reasoning: 'Working through hard problems',
        long_context: 'Reading long documents',
        vision: 'Looking at images',
        agentic: 'Multi-step tasks with tools',
      },
      loading: 'Working out what fits this computer…',
      failed: (message: string) => `The advisor could not work out its recommendations: ${message}`,
      pickOne: 'Pick at least one thing above.',
      fetchList: 'Fetch the model list (about 10 MB)',
      fetching: 'Fetching the model list from Hugging Face… this takes a minute or two.',
      fetchFailed: (message: string) => `The model list could not be fetched: ${message}`,
      whyNotUsed: 'Why is my graphics card not used?',
      current: (name: string) => `You have ${name}.`,
      listLabel: 'Recommended models, best first',
      nameInOllama: 'Its name in Ollama',
      speed: 'Speed',
      noSpeed: 'no estimate yet',
      memory: 'Memory it needs',
      download: 'Download',
      installed: 'already on this computer',
      keepsInMind: (words: string) => `Set up to keep about ${words} words in mind at once`,
      why: 'Why this one',
      explainer: 'Why?',
      confidence: {
        high: 'High confidence',
        medium: 'Medium confidence',
        low: 'Low confidence',
      },
      advanced: {
        title: 'Technical details',
        term: 'Term',
        value: 'Value',
        explain: 'What it is',
        weights: { label: 'Weights', explain: 'The model itself, loaded into memory (plus its image reader, when it has one).' },
        kvCache: { label: 'KV cache', explain: 'The memory that holds the conversation so far; it grows with the context.' },
        overhead: { label: 'Runtime overhead', explain: 'What Ollama needs for itself on this kind of graphics.' },
        total: { label: 'Total', explain: 'Everything above, added up.' },
        gpuResident: { label: 'On the graphics', explain: 'The part that lives in graphics memory.' },
        cpuOffload: { label: 'In ordinary memory (offload)', explain: 'The part that lives in the computer\'s ordinary memory instead.' },
        budget: { label: 'Compared against', explain: 'The memory this computer can give a model.' },
        context: { label: 'Context window', explain: 'How many tokens (word pieces) the model keeps in mind at once.' },
        quantization: { label: 'Quantization', explain: 'How much the model\'s numbers were compressed to make the file smaller.' },
        generation: { label: 'Generation', explain: 'Tokens per second while answering; a token is about three quarters of a word.' },
        prompt: { label: 'Prompt processing', explain: 'Tokens per second while reading what you sent.' },
        path: { label: 'Runtime path', explain: 'How Ollama drives this computer\'s graphics — or "cpu" when it does not.' },
        pathExpected: 'expected; not seen yet',
        pathEstablished: 'seen after a load',
        category: { label: 'Fit', explain: 'The comparison that decided whether it fits.' },
        speedBasis: { label: 'Speed estimated from', explain: 'The memory speed of this computer and how much of it models usually achieve.' },
        score: { label: 'Ranking score', explain: 'Purpose × fit × speed × size; only the order matters.' },
        scoreValue: (score: number, p: number, f: number, sp: number, z: number) =>
          `${score} = purpose ${p} × fit ${f} × speed ${sp} × size ${z}`,
        notes: 'Notes',
        tokens: (n: string) => `${n} tokens`,
      },
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
