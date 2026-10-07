# Архитектурный Алгоритм PRZM

Этот документ описывает полный цикл управления потоком данных (Control Flow) при сжатии и распаковке алгоритмом PRZM. Алгоритм спроектирован для достижения скорости распаковки уровня оперативной памяти (10-50+ ГБ/с) со строгим соблюдением правила 0.0 выделений памяти (zero heap allocations) при декодировании.

## Полная Блок-Схема (Mermaid)

```mermaid
flowchart TD
    classDef startend fill:#2c3e50,stroke:#34495e,stroke-width:2px,color:#fff
    classDef loop fill:#f39c12,stroke:#e67e22,stroke-width:2px,color:#fff
    classDef decision fill:#8e44ad,stroke:#9b59b6,stroke-width:2px,color:#fff
    classDef action fill:#2980b9,stroke:#3498db,stroke-width:2px,color:#fff
    classDef memory fill:#27ae60,stroke:#2ecc71,stroke-width:2px,color:#fff

    Start([Вход: byte[] data]) :::startend

    %% --- ENCODER ---
    subgraph ENCODER [АЛГОРИТМ КОМПРЕССОРА (Параллельно по блокам)]
        ChunkLoop{"Цикл: разбить data\nна блоки (64KB - 8MB)"} :::loop
        
        %% Phase 1: Routing
        Heuristic["Сканирование 256 байт\n(Подсчёт энтропии и ASCII)"] :::action
        CheckRouter{"Категория данных?"} :::decision
        
        IsB64["Применить SIMD Base64 Decode -> raw_bin.\nЗаписать заголовок: T_B64"] :::action
        IsPE["Применить BCJ фильтр (x86/ARM).\nЗаписать заголовок: T_BCJ"] :::action
        IsMath["Применить Shuffle/BitPlane.\nЗаписать заголовок: T_MATH"] :::action
        IsText["Оставить как есть.\nЗаписать заголовок: T_RAW"] :::action

        %% Phase 2: LZ77 + DAG
        HashWindow["Инициализация Хэш-таблицы (L3 Cache)\nПоиск совпадений (MinMatch=4)"] :::action
        DAG["Построение графа возможных совпадений (DAG)\nОценка битовой стоимости путей"] :::action
        DP["Динамическое программирование (с конца в начало):\nВыбор кратчайшего пути"] :::action
        
        %% Phase 3: Registers
        RepLoop{"Цикл: для каждого найденного\nсмещения (offset)"} :::loop
        CheckRep{"offset == rep0, rep1, rep2?"} :::decision
        MakeRep0["Генерировать токен: REP_0"] :::action
        MakeRep1["Генерировать токен: REP_1\nSwap(rep0, rep1)"] :::action
        MakeNew["Генерировать токен: MATCH(offset)\nrep2=rep1, rep1=rep0, rep0=offset"] :::action

        %% Phase 4: Entropy
        BuildHist["Построение гистограммы частот.\nГенерация Canonical Huffman Tree"] :::action
        CheckGain{"Сжатый размер < Оригинал?"} :::decision
        WriteFlat["Установить флаг IS_FLAT = 1.\nЗаписать массивы без сжатия"] :::action
        WriteHuff["Установить IS_FLAT = 0.\nЗаписать 149 байт заголовка дерева.\nУпаковать в 4 битовых потока"] :::action
    end

    %% Encoder Flow
    Start --> ChunkLoop
    ChunkLoop --> Heuristic
    Heuristic --> CheckRouter
    CheckRouter -- "Base64/Hex" --> IsB64
    CheckRouter -- "exe/dll" --> IsPE
    CheckRouter -- "Медиа/Массивы" --> IsMath
    CheckRouter -- "Текст/Код" --> IsText
    
    IsB64 & IsPE & IsMath & IsText --> HashWindow
    HashWindow --> DAG --> DP --> RepLoop
    
    RepLoop --> CheckRep
    CheckRep -- Да, rep0 --> MakeRep0
    CheckRep -- Да, rep1/2 --> MakeRep1
    CheckRep -- Нет --> MakeNew
    MakeRep0 & MakeRep1 & MakeNew --> RepLoop
    
    RepLoop -- "Конец списка" --> BuildHist
    BuildHist --> CheckGain
    CheckGain -- Нет --> WriteFlat
    CheckGain -- Да --> WriteHuff
    
    WriteFlat & WriteHuff --> EndEnc([Выход: byte[] compressed]) :::startend

    %% --- DECODER ---
    subgraph DECODER [АЛГОРИТМ ДЕКОДЕРА (Строго 0 Аллокаций кучи)]
        DecStart([Вход: byte[] compressed, предвыделенный byte[] out]) :::startend
        DecLoop{"Цикл: Чтение заголовка блока"} :::loop
        
        %% Entropy Decode
        DecCheckFlat{"IS_FLAT == 1?"} :::decision
        DecFlat["Memcpy(compressed -> out)"] :::memory
        DecHuff["Прочитать 149 байт.\nПостроить LUT (Look-up Table) в стэке.\nРаспаковать 4 потока в массивы: Lits, Lens, Offs"] :::action
        
        %% LZ Reconstruction
        ReconLoop{"Цикл: пока не кончатся\nтокены (Lits/Lens/Offs)"} :::loop
        ReconCheck{"Тип токена?"} :::decision
        
        DoLit["out[pos++] = Lits[i++]"] :::memory
        DoRep["Взять смещение из rep0/1/2.\nСкопировать Lens байт из (pos-rep) в pos"] :::memory
        DoMatch["Взять новое смещение.\nОбновить регистры: rep2=rep1, rep1=rep0, rep0=offset.\nСкопировать Lens байт"] :::memory
        
        %% Math Inversion
        InvMath{"Проверка T_B64, T_BCJ, T_MATH"} :::decision
        DoInv["Применить обратные SIMD-инструкции\nпрямо в буфере out"] :::action
    end

    %% Decoder Flow
    EndEnc -. "Передача файла" .-> DecStart
    DecStart --> DecLoop
    DecLoop --> DecCheckFlat
    DecCheckFlat -- Да --> DecFlat
    DecCheckFlat -- Нет --> DecHuff
    
    DecFlat & DecHuff --> ReconLoop
    ReconLoop --> ReconCheck
    ReconCheck -- Literal --> DoLit
    ReconCheck -- Rep(0/1/2) --> DoRep
    ReconCheck -- Match --> DoMatch
    
    DoLit & DoRep & DoMatch --> ReconLoop
    ReconLoop -- "Блок собран" --> InvMath
    InvMath -- "Есть фильтр" --> DoInv
    InvMath -- "RAW" --> DecLoop
    DoInv --> DecLoop
    
    DecLoop -- "EOF" --> Final([Готово]) :::startend
```

## Ключевые инженерные контракты (C/Rust/Zig)

1. **Предвыделение (Pre-allocation):** Функция распаковки принимает `uint8_t* out`. Запрещены вызовы `malloc/new`.
2. **Локальность таблиц Хаффмана:** LUT строится исключительно на стеке локального потока для удержания в L1 кэше процессора.
3. **Безопасное перекрытие (Overlapping Memcpy):** Копирование LZ-токенов должно корректно обрабатывать ситуации, когда смещение меньше длины копируемого фрагмента.
