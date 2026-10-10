# PRZM Architectural Algorithm

This document describes the complete Control Flow during compression and decompression by the PRZM algorithm. The algorithm is designed to achieve RAM-level decompression speeds (10-50+ GB/s) strictly adhering to the 0.0 heap allocations rule during decoding.

## Complete Flowchart (Mermaid)

```mermaid
flowchart TD
    classDef startend fill:#2c3e50,stroke:#34495e,stroke-width:2px,color:#fff
    classDef loop fill:#f39c12,stroke:#e67e22,stroke-width:2px,color:#fff
    classDef decision fill:#8e44ad,stroke:#9b59b6,stroke-width:2px,color:#fff
    classDef action fill:#2980b9,stroke:#3498db,stroke-width:2px,color:#fff
    classDef memory fill:#27ae60,stroke:#2ecc71,stroke-width:2px,color:#fff

    Start([Input: byte[] data]) :::startend

    %% --- ENCODER ---
    subgraph ENCODER [COMPRESSOR ALGORITHM (Parallel by chunks)]
        ChunkLoop{"Loop: split data\ninto chunks (64KB - 8MB)"} :::loop
        
        %% Phase 1: Routing
        Heuristic["Scan 256 bytes\n(Compute entropy & ASCII)"] :::action
        CheckRouter{"Data category?"} :::decision
        
        IsB64["Apply SIMD Base64 Decode -> raw_bin.\nWrite header: T_B64"] :::action
        IsPE["Apply BCJ filter (x86/ARM).\nWrite header: T_BCJ"] :::action
        IsMath["Apply Shuffle/BitPlane.\nWrite header: T_MATH"] :::action
        IsText["Leave as is.\nWrite header: T_RAW"] :::action

        %% Phase 2: LZ77 + DAG
        HashWindow["Initialize Hash Table (L3 Cache)\nSearch for matches (MinMatch=4)"] :::action
        DAG["Build match DAG\nEvaluate bit-cost of paths"] :::action
        DP["Dynamic Programming (back-to-front):\nSelect shortest path"] :::action
        
        %% Phase 3: Registers
        RepLoop{"Loop: for each found\noffset"} :::loop
        CheckRep{"offset == rep0, rep1, rep2?"} :::decision
        MakeRep0["Generate token: REP_0"] :::action
        MakeRep1["Generate token: REP_1\nSwap(rep0, rep1)"] :::action
        MakeNew["Generate token: MATCH(offset)\nrep2=rep1, rep1=rep0, rep0=offset"] :::action

        %% Phase 4: Entropy
        BuildHist["Build frequency histogram.\nGenerate Canonical Huffman Tree"] :::action
        CheckGain{"Compressed size < Original?"} :::decision
        WriteFlat["Set IS_FLAT = 1.\nWrite arrays uncompressed"] :::action
        WriteHuff["Set IS_FLAT = 0.\nWrite 149-byte tree header.\nPack into 4 bitstreams"] :::action
    end

    %% Encoder Flow
    Start --> ChunkLoop
    ChunkLoop --> Heuristic
    Heuristic --> CheckRouter
    CheckRouter -- "Base64/Hex" --> IsB64
    CheckRouter -- "exe/dll" --> IsPE
    CheckRouter -- "Media/Arrays" --> IsMath
    CheckRouter -- "Text/Code" --> IsText
    
    IsB64 & IsPE & IsMath & IsText --> HashWindow
    HashWindow --> DAG --> DP --> RepLoop
    
    RepLoop --> CheckRep
    CheckRep -- "Yes, rep0" --> MakeRep0
    CheckRep -- "Yes, rep1/2" --> MakeRep1
    CheckRep -- "No" --> MakeNew
    MakeRep0 & MakeRep1 & MakeNew --> RepLoop
    
    RepLoop -- "End of list" --> BuildHist
    BuildHist --> CheckGain
    CheckGain -- "No" --> WriteFlat
    CheckGain -- "Yes" --> WriteHuff
    
    WriteFlat & WriteHuff --> EndEnc([Output: byte[] compressed]) :::startend

    %% --- DECODER ---
    subgraph DECODER [DECODER ALGORITHM (Strictly 0 Heap Allocations)]
        DecStart([Input: byte[] compressed, pre-allocated byte[] out]) :::startend
        DecLoop{"Loop: Read block header"} :::loop
        
        %% Entropy Decode
        DecCheckFlat{"IS_FLAT == 1?"} :::decision
        DecFlat["Memcpy(compressed -> out)"] :::memory
        DecHuff["Read 149 bytes.\nBuild stack LUT (Look-up Table).\nUnpack 4 streams into arrays: Lits, Lens, Offs"] :::action
        
        %% LZ Reconstruction
        ReconLoop{"Loop: while tokens\nremain (Lits/Lens/Offs)"} :::loop
        ReconCheck{"Token type?"} :::decision
        
        DoLit["out[pos++] = Lits[i++]"] :::memory
        DoRep["Get offset from rep0/1/2.\nCopy Lens bytes from (pos-rep) to pos"] :::memory
        DoMatch["Get new offset.\nUpdate registers: rep2=rep1, rep1=rep0, rep0=offset.\nCopy Lens bytes"] :::memory
        
        %% Math Inversion
        InvMath{"Check T_B64, T_BCJ, T_MATH"} :::decision
        DoInv["Apply inverse SIMD instructions\ndirectly in 'out' buffer"] :::action
    end

    %% Decoder Flow
    EndEnc -. "File transfer" .-> DecStart
    DecStart --> DecLoop
    DecLoop --> DecCheckFlat
    DecCheckFlat -- "Yes" --> DecFlat
    DecCheckFlat -- "No" --> DecHuff
    
    DecFlat & DecHuff --> ReconLoop
    ReconLoop --> ReconCheck
    ReconCheck -- "Literal" --> DoLit
    ReconCheck -- "Rep(0/1/2)" --> DoRep
    ReconCheck -- "Match" --> DoMatch
    
    DoLit & DoRep & DoMatch --> ReconLoop
    ReconLoop -- "Block reconstructed" --> InvMath
    InvMath -- "Has filter" --> DoInv
    InvMath -- "RAW" --> DecLoop
    DoInv --> DecLoop
    
    DecLoop -- "EOF" --> Final([Done]) :::startend
```

## Key Engineering Contracts (C/Rust/Zig)

1. **Pre-allocation:** The decompression function accepts `uint8_t* out`. Calls to `malloc/new` are forbidden.
2. **Huffman table locality:** The LUT is built exclusively on the local thread stack to keep it inside the CPU L1 cache.
3. **Safe overlapping (Overlapping Memcpy):** Copying LZ tokens must correctly handle situations where the offset is smaller than the length of the copied fragment.
