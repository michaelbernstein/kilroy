# Rogue 5.4.4 WebAssembly Port — Definition of Done

## Scope

### In Scope

Everything required to deliver a faithful, browser-playable Rogue 5.4.4 port at `demo/rogue/rogue-wasm/www/index.html`, including wizard mode as both a gameplay feature and the primary integration test harness.

### Out of Scope

Score file encryption, wizard password authentication (replaced by simpler browser gate), mobile input, graphical tiles, multiplayer, accessibility beyond the original, cross-session networked leaderboards, native binary deliverable, signal handling. See `spec.md` Non-Goals for full list.

### Assumptions

- Rust stable toolchain, `cargo`, `rustc`, and `wasm-pack` are installed.
- Browser target: latest two versions of Chrome, Firefox, Safari, Edge.
- Original C source at `demo/rogue/original-rogue/` is the authoritative reference.

---

## Deliverables

| Artifact | Path | Description |
|----------|------|-------------|
| WASM Rust crate | `demo/rogue/rogue-wasm/` | Rust project with `Cargo.toml`, `src/`, targeting `wasm32-unknown-unknown` |
| HTML page | `demo/rogue/rogue-wasm/www/index.html` | Single-page deliverable that loads the WASM module and renders the game |
| JS glue | `demo/rogue/rogue-wasm/www/` | Minimal JS to bridge WASM ↔ browser (keyboard, rendering, localStorage) |
| Tests | `demo/rogue/rogue-wasm/src/` or `tests/` | Rust tests: unit tests for game systems + integration tests for gameplay scenarios |

---

## Acceptance Criteria

### AC-1: Build and Tooling

1. `cd demo/rogue/rogue-wasm && cargo build --target wasm32-unknown-unknown` exits 0 with no errors.
2. `cd demo/rogue/rogue-wasm && cargo fmt --check` exits 0 (no formatting violations).
3. `cd demo/rogue/rogue-wasm && cargo test` exits 0 (all unit and integration tests pass on native target).
4. `Cargo.toml` exists with a `[lib]` target of type `cdylib` (required for wasm-pack/WASM).
5. No `unsafe` blocks unless documented with a justification comment explaining why safe alternatives are insufficient.

### AC-2: Deliverable Integrity

1. `demo/rogue/rogue-wasm/www/index.html` exists and is a valid HTML file.
2. The HTML file references a `.wasm` file (directly or via JS glue).
3. The HTML file contains or references a monospace-font 80x24 character grid renderer.
4. The HTML file can be served with a static file server (e.g., `python -m http.server`) and loads without errors in browser devtools console.
5. No external CDN dependencies — all required JS/WASM is local.

### AC-3: Display Fidelity

1. The terminal grid is exactly 80 columns by 24 rows (where row 24 is the status line, matching `NUMCOLS=80`, `NUMLINES=24`, `STATLINE=NUMLINES-1`).
2. The player is rendered as `@`.
3. Corridors render as `#`, floors as `.`, doors as `+`, stairs as `%`, traps as `^`.
4. Gold renders as `*`, potions as `!`, scrolls as `?`, wands/staves as `/`, weapons as `)`, armor as `]`, rings as `=`, amulet as `,`, food as `:`.
5. Monsters render as their uppercase letter (A-Z), matching the `extern.c` monster table.
6. Dark background with light text (the classic Rogue aesthetic).
7. The status line displays: dungeon level, HP (current/max), strength (current/max), gold, armor class, experience level, and experience points — matching the original's `status()` format.

### AC-4: Input Handling

1. All movement keys work: `h`/`j`/`k`/`l` (cardinal), `y`/`u`/`b`/`n` (diagonal).
2. Shift+movement keys produce "run" behavior (move until interrupted).
3. Ctrl+movement keys produce "run and stop at door/corridor" behavior.
4. All item action keys work: `q` (quaff), `r` (read), `e` (eat), `w` (wield), `W` (wear), `P` (put on ring), `R` (remove ring), `d` (drop), `t` (throw), `z` (zap wand).
5. All meta keys work: `i` (inventory), `I` (single item), `s` (search), `>` (descend), `<` (ascend), `.` (rest), `f` (fight to death), `c` (call/name), `/` (identify character), `?` (help).
6. Numeric prefix for command repetition works (e.g., `5s` searches 5 times).
7. The `S` key triggers save to localStorage. The `Q` key triggers quit with confirmation.
8. Browser keyboard events (keydown/keypress) do not propagate to cause unintended behavior (scrolling, browser shortcuts).

### AC-5: RNG Fidelity

1. The RNG implements the exact LCG: `seed = seed * 11109 + 13849`, output = `(seed >> 16) & 0xffff`.
2. `rnd(range)` returns `abs(RN) % range`, and returns 0 when range is 0.
3. `roll(number, sides)` returns the sum of `number` calls to `rnd(sides) + 1`.
4. A deterministic seed mode is exposed (equivalent to the C version's `SEED` env var) so that a game can be started with a known seed for reproducible testing.
5. **Verification test**: Seed with value `12345`, call `rnd(100)` 20 times, and verify the sequence matches the C implementation's output for the same seed. This test must be automated in `cargo test`.
6. **Verification test**: Seed with value `12345`, call `roll(3, 6)` 10 times, verify sequence matches C.

### AC-6: Dungeon Generation Fidelity

1. Room placement uses a 3x3 grid with `MAXROOMS=9`.
2. Room dimensions derived from `bsze.x = NUMCOLS/3`, `bsze.y = NUMLINES/3`.
3. `rnd(4)` rooms per level are marked ISGONE (removed).
4. Dark room probability: `rnd(10) < level - 1`.
5. Maze room probability: `rnd(15) == 0` (only when room would be dark).
6. Passage connectivity uses spanning tree over the hardcoded `rdes[]` adjacency graph.
7. Trap placement: `rnd(10) < level` triggers traps; count = `rnd(level/4) + 1`, capped at `MAXTRAPS=10`.
8. All 8 trap types exist: door, arrow, sleep, bear, teleport, dart, rust, mystery.
9. **Verification test**: Seed a known value, generate level 5, verify room count, dimensions, and passage layout match C output. This test must be automated in `cargo test`.

### AC-7: Monster Fidelity

1. All 26 monsters (A-Z) exist with stats matching `extern.c`: name, carry%, flags, strength, experience, level, armor, HP formula, damage string.
2. Monster HP = `roll(monster_level, 8)`.
3. Monster level scaling beyond `AMULETLEVEL=26`: `lev_add = max(0, level - 26)`, monster level += lev_add, armor -= lev_add.
4. Monster selection uses `lvl_mons[]` (wandering) and `wand_mons[]` (non-wandering) arrays with formula: `d = level + rnd(10) - 6`, clipped to 0-25.
5. Mean monsters have 66.7% chance to chase on sight (`rnd(3) != 0`).
6. All special abilities implemented: aquator rust, ice monster freeze, rattlesnake poison, wraith exp drain, vampire max HP drain, venus flytrap hold, leprechaun gold steal, nymph item steal, medusa confusion gaze, xeroc disguise.
7. `ISFLY` monsters can move twice per turn when distance >= 3.
8. `ISREGEN` monsters heal 1 HP per `doctor()` call.
9. `ISINVIS` monsters require see invisible to detect.
10. **Verification test**: Instantiate each of the 26 monsters, verify all stat fields match the C table. Automated in `cargo test`.

### AC-8: Combat Fidelity

1. To-hit formula: `rnd(20) + wplus >= (20 - at_lvl) - op_arm`.
2. Damage: `roll(ndice, nsides) + dplus + add_dam[str]`.
3. Strength bonus tables (`str_plus[]` for to-hit, `add_dam[]` for damage) match the C arrays exactly.
4. Sleeping/held target bonus: +4 to hit.
5. Ring bonuses: `R_ADDHIT` affects to-hit, `R_ADDDAM` affects damage.
6. Save throw formula: `roll(1, 20) >= 14 + which - level/2`.
7. Save categories: `VS_POISON=0`, `VS_BREATH=2`, `VS_MAGIC=3`.
8. **Verification test**: Verify `swing(5, 3, 2)` for 1000 iterations produces hit rate within 1% of C implementation. Automated in `cargo test`.

### AC-9: Item System Fidelity

1. Item type generation uses weights: potion 26%, scroll 36%, food 16%, weapon 7%, armor 7%, ring 4%, stick 4%.
2. All 14 potion types with correct probabilities and effects (confusion, hallucination, poison, strength, see invisible, healing, monster detection, magic detection, raise level, extra healing, haste, restore strength, blindness, levitation).
3. All 18 scroll types with correct probabilities and effects (confusion, mapping, hold monster, sleep, enchant armor, 5x identify, scare monster, food detection, teleport, enchant weapon, create monster, remove curse, aggravate, protect armor).
4. All 9 weapon types with correct wield and throw damage dice.
5. All 8 armor types with correct AC values and generation probabilities.
6. All 14 ring types with correct effects and probabilities.
7. All 14 wand/staff types with correct effects, probabilities, and charge ranges.
8. Gold calculation: `rnd(50 + 10 * level) + 2`.
9. Per-level item generation: 9 attempts, each at 36% (`rnd(100) < 36`).
10. Inventory limit: `MAXPACK=23`.
11. **Verification test**: Verify item type probability distribution over 100,000 rolls is within 2% of expected weights. Automated in `cargo test`.

### AC-10: Timing and Daemon Fidelity

1. Daemon/fuse queue supports `MAXDAEMONS=20` entries.
2. All four starting daemons run: `runners` (monster AI), `doctor` (healing), `swander` (wandering monster timer), `stomach` (hunger).
3. BEFORE/AFTER execution order matches the `spread()` formula.
4. Hunger system: `HUNGERTIME=1300`, `STARVETIME=850`, `STOMACHSIZE=2000`.
5. Healing daemon: quiet >= 3 turns triggers healing.
6. Wandering monster timer: `WANDERTIME=spread(70)` — randomized interval of 67-73 turns between spawn events (using the `spread()` formula: `nm - nm/20 + rnd(nm/10)`).

### AC-11: Save and Load

1. `S` key serializes full game state to browser localStorage.
2. State includes: player stats/position, inventory, current level map, monster positions/states, daemon/fuse queue, room states, discovered item names, RNG seed.
3. On page load, if a save exists in localStorage, the game offers to restore it.
4. Restored game is playable and behaves identically to a continued session (same RNG sequence, same monster states).
5. Save data is keyed to avoid collisions with other localStorage users (e.g., prefix `rogue_save_`).

### AC-12: Player Progression

1. Starting stats: STR 16, EXP 0, LVL 1, ARM 10, HP 12, MaxHP 12, damage "1x4".
2. Starting equipment: +1 mace, ring mail, +1 bow, 25-40 arrows, 1 food ration.
3. Experience level thresholds match `e_levels[]` from `extern.c` (21 breakpoints, L1 through L20+).
4. Amulet of Yendor appears on level 26 (`AMULETLEVEL`).
5. Scoring and death screen (tombstone) display correctly.

### AC-13: Wizard Mode

1. Wizard mode can be activated via a browser-friendly mechanism (URL parameter, JS API call, or `+` key with simplified gate — no `crypt()` password required).
2. `C` key (wizard): Create any object — prompts for type, which, blessing. Object appears in inventory.
3. `Ctrl+D` (wizard): Advance to next dungeon level (`level++; new_level()`).
4. `Ctrl+A` (wizard): Go back to previous dungeon level (`level--; new_level()`).
5. `Ctrl+T` (wizard): Teleport player to random floor tile in current level.
6. `Ctrl+F` (wizard): Reveal full level map (all rooms, passages, items, monsters visible).
7. `Ctrl+W` (wizard): Identify any item in inventory (reveal true name and enchantment).
8. `Ctrl+E` (wizard): Display remaining food counter.
9. `|` (wizard): Display current player coordinates.
10. `$` (wizard): Display current pack item count.
11. `Ctrl+G` (wizard): Show inventory of all objects on the current level.
12. `Ctrl+X` (wizard): Toggle see-monsters mode (reveal all monsters on level).
13. `Ctrl+~` (wizard): Set wand charges to 10000 (infinite charges for testing).
14. `noscore = TRUE` when wizard mode is active (wizard games don't record scores).
15. Wizard mode can be toggled off via `+` key (restores normal play, `noscore` stays true).

### AC-14: Gameplay Integration Tests (The Game Works)

These tests drive the game engine programmatically with a fixed seed, executing multi-turn sequences and asserting game state. They prove the game actually works end-to-end, not just that individual systems are correctly wired. All must be automated in `cargo test`.

#### Scenario 1: Basic Exploration (seed-deterministic)

Set a fixed seed. Initialize a new game. Verify:
1. Player starts at `@` on level 1, in a lit room.
2. Player has starting equipment (mace, ring mail, bow, arrows, food).
3. Status line shows Level:1, HP:12(12), Str:16(16), Gold:0, Arm:4, Exp:1/0.
4. Execute 10 movement commands (e.g., `h`, `l`, `j`, `k` sequence). After each, verify player position changed and is on a walkable tile (floor, door, or corridor).
5. The dungeon contains at least 1 room with floor tiles, at least 1 corridor, and at least 1 staircase.

#### Scenario 2: Combat and XP (wizard-assisted, seed-deterministic)

Set a fixed seed. Initialize game. Use wizard mode:
1. Use `Ctrl+D` to advance to level 3 (monsters are more common).
2. Verify the level map contains at least one monster (any A-Z character).
3. Move the player adjacent to a monster (using programmatic movement or `Ctrl+T` to teleport near one).
4. Execute `f` (fight to death) or directional attack toward the monster.
5. Verify: at least one "hit" or "miss" message was generated, combat used the `swing()` formula, and if the monster died, player XP increased by the monster's experience value.

#### Scenario 3: Item Pickup and Inventory (wizard-assisted)

Set a fixed seed. Initialize game. Use wizard mode:
1. Use `C` to create a potion of healing (type=`!`, which=5, blessing=`n`).
2. Verify the potion appears in inventory (`i` command lists it).
3. Use `q` to quaff the potion. Verify HP is restored (or stays at max if already full) and the potion is consumed from inventory.
4. Use `C` to create a scroll of identify (type=`?`, which=5, blessing=`n`).
5. Use `C` to create an unidentified weapon.
6. Use `r` to read the scroll, then select the weapon. Verify the weapon's true name (enchantment) is now revealed.

#### Scenario 4: Dungeon Descent and Level Generation

Set a fixed seed. Initialize game. Use wizard mode:
1. Use `Ctrl+D` to descend 5 times (arrive at level 6).
2. At each level, verify: rooms exist, corridors connect rooms, stairs are present.
3. Verify monster difficulty roughly scales: level 6 monsters should have higher average stats than level 1.
4. Use `Ctrl+F` to reveal the full map at level 6. Verify the map contains rooms, corridors, and at least one monster.

#### Scenario 5: Hunger and Starvation

Set a fixed seed. Initialize game:
1. Record initial `food_left` (should be `HUNGERTIME=1300` equivalent after eating starting ration).
2. Execute `.` (rest) for 100 turns. Verify `food_left` decreased by approximately 100.
3. Use wizard `Ctrl+E` to verify the food counter.
4. If feasible within test budget: rest until "hungry" message appears, then rest more until "weak" message, confirming the hunger state machine transitions at correct thresholds.

#### Scenario 6: Save, Restore, and Deterministic Continuation

Set a fixed seed. Initialize game:
1. Play for 20 turns (mix of movement and rest).
2. Record full game state snapshot: player position, HP, XP, inventory count, current level, RNG seed.
3. Trigger save.
4. Create a new game instance and trigger restore from the saved state.
5. Verify all snapshot fields match the restored game state exactly.
6. Play 10 more turns on the restored game. Verify the game does not crash and state continues to evolve correctly.

#### Scenario 7: Special Monster Abilities (wizard-assisted)

Set a fixed seed. Initialize game. Use wizard mode:
1. Create plate mail armor, wear it.  Use `C` to summon an Aquator (A). Engage in combat. Verify armor is rusted (AC value degraded) after being hit.
2. Use `C` to summon a Rattlesnake (R). Get hit. Verify strength decreases (if save throw fails) or stays same (if save succeeds), matching the `save(VS_POISON)` formula.
3. Use `C` to summon a Leprechaun (L). Get hit when carrying gold. Verify gold is stolen.

#### Scenario 8: Wand Usage (wizard-assisted)

Set a fixed seed. Initialize game. Use wizard mode:
1. Use `C` to create a wand of teleport-away (type=`/`, which=11).
2. Ensure a monster is visible. Zap the wand at the monster (`z` + direction).
3. Verify the monster is teleported to a different location (no longer adjacent or visible in original position).
4. Use `C` to create a wand of lightning (type=`/`, which=2).
5. Zap at a monster. Verify the bolt damages or kills the monster.

#### Scenario 9: Ring Effects (wizard-assisted)

Set a fixed seed. Initialize game. Use wizard mode:
1. Use `C` to create a ring of regeneration (type=`=`, which=9).
2. `P` to put on the ring. Take some damage (fight a monster). Then rest for 10 turns.
3. Verify HP regeneration rate is faster than without the ring.
4. Use `C` to create a ring of see invisible (type=`=`, which=4).
5. `P` to put it on. Use `C` to summon a Phantom (P, ISINVIS). Verify the Phantom is visible.

#### Scenario 10: Scroll Effects (wizard-assisted)

Set a fixed seed. Initialize game. Use wizard mode:
1. Use `C` to create a scroll of magic mapping (type=`?`, which=1).
2. `r` to read it. Verify the entire level map is revealed (equivalent to `Ctrl+F` output).
3. Use `C` to create a scroll of enchant weapon (type=`?`, which=13).
4. Wield a weapon, read the scroll. Verify the weapon's damage bonus (`o_dplus`) increased by 1.
5. Use `C` to create a scroll of teleportation (type=`?`, which=12).
6. Record player position. Read the scroll. Verify player position changed.

#### Scenario 11: Death and Scoring

Set a fixed seed. Initialize game. Use wizard mode:
1. Use `Ctrl+D` to descend to level 10.
2. Reduce player HP to near-death (fight strong monsters or take repeated damage).
3. Die to a monster. Verify:
   - The tombstone / death screen appears.
   - The death message names the monster that killed the player.
   - The score is displayed (based on XP and gold collected).
   - Since wizard mode is active, `noscore` is true and the score is not recorded to any persistent leaderboard.

---

## Verification Steps

### Deterministic Checks (CI-automatable)

```bash
# 1. Format check
cd demo/rogue/rogue-wasm && cargo fmt --check

# 2. Build for WASM
cd demo/rogue/rogue-wasm && cargo build --target wasm32-unknown-unknown

# 3. All tests (unit + integration gameplay tests)
cd demo/rogue/rogue-wasm && cargo test

# 4. Deliverable existence
test -f demo/rogue/rogue-wasm/www/index.html

# 5. HTML references WASM/monospace
grep -q 'wasm\|\.wasm' demo/rogue/rogue-wasm/www/index.html
grep -qi 'monospace\|courier\|mono' demo/rogue/rogue-wasm/www/index.html

# 6. 80x24 grid reference
grep -qE '80.?x.?24|NUMCOLS.*80|cols.*80|width.*80' demo/rogue/rogue-wasm/www/index.html

# 7. No artifact pollution (tracked or untracked)
! { git diff --name-only; git ls-files --others --exclude-standard; } | grep -qE '(^|/)(target|node_modules|dist|\.cargo-target|\.cargo_target|pkg)(\/|$)'
```

### Required Unit Tests (in `cargo test`)

These tests validate mechanical fidelity of individual game systems against the C original:

| Test Name | What It Verifies |
|-----------|------------------|
| `test_rng_sequence` | LCG output for seed 12345 matches C for 20 calls to `rnd(100)` |
| `test_roll_sequence` | `roll(3,6)` for seed 12345 matches C for 10 calls |
| `test_monster_stats_table` | All 26 monster stat blocks match `extern.c` values |
| `test_combat_swing` | `swing()` hit/miss for specific (level, armor, bonus) inputs matches C |
| `test_item_type_distribution` | 100k item rolls produce weights within 2% of 26/36/16/7/7/4/4 |
| `test_dungeon_room_count` | Seed 12345, level 5: room count and ISGONE flags match C |
| `test_strength_tables` | `str_plus[]` and `add_dam[]` arrays match C exactly |
| `test_armor_ac_values` | All 8 armor types have correct base AC |
| `test_weapon_damage_tables` | All 9 weapons have correct wield and throw damage dice |
| `test_hunger_constants` | HUNGERTIME, STARVETIME, STOMACHSIZE match C |
| `test_experience_levels` | `e_levels[]` thresholds match C for all 21 breakpoints |
| `test_potion_probabilities` | All 14 potion types have correct generation weights |
| `test_scroll_probabilities` | All 18 scroll types have correct generation weights |
| `test_ring_probabilities` | All 14 ring types have correct generation weights |
| `test_wand_probabilities` | All 14 wand types have correct generation weights |
| `test_gold_calculation` | `GOLDCALC` for known seed/level matches C |
| `test_save_throw` | Save throw DC for known inputs matches C formula |
| `test_trap_types` | All 8 trap types exist with correct IDs |

### Required Integration Tests (in `cargo test`)

These tests validate that the game actually works as an integrated system — see AC-14 for full scenario definitions:

| Test Name | What It Proves |
|-----------|---------------|
| `test_gameplay_basic_exploration` | Game initializes, player moves, dungeon is populated (Scenario 1) |
| `test_gameplay_combat_and_xp` | Combat resolves, monsters die, XP is awarded (Scenario 2) |
| `test_gameplay_item_pickup_and_use` | Items can be created, picked up, used, and consumed (Scenario 3) |
| `test_gameplay_dungeon_descent` | Level generation works across 5+ levels, difficulty scales (Scenario 4) |
| `test_gameplay_hunger` | Hunger counter decreases, hunger messages appear at correct thresholds (Scenario 5) |
| `test_gameplay_save_restore` | Full game state round-trips through save/restore identically (Scenario 6) |
| `test_gameplay_monster_specials` | Aquator rusts armor, rattlesnake poisons, leprechaun steals gold (Scenario 7) |
| `test_gameplay_wand_usage` | Wands fire correctly, teleport-away moves monsters, lightning damages (Scenario 8) |
| `test_gameplay_ring_effects` | Regeneration ring heals faster, see-invisible ring reveals phantoms (Scenario 9) |
| `test_gameplay_scroll_effects` | Magic mapping reveals level, enchant weapon improves stats, teleport moves player (Scenario 10) |
| `test_gameplay_death_and_scoring` | Player can die, tombstone shows, score is calculated (Scenario 11) |

### Semantic Review Checklist

A human reviewer (or LLM reviewer with code access) must verify:

- [ ] The Rust code structure mirrors the C module organization (or has a documented mapping)
- [ ] Each C function with gameplay logic has a corresponding Rust function
- [ ] No "placeholder" or "TODO" implementations for any game system listed in scope
- [ ] The JS terminal renderer properly handles all display characters listed in AC-3
- [ ] Keyboard input works for the full command set in AC-4 (manual browser test)
- [ ] Wizard mode activates and all wizard commands from AC-13 function in the browser
- [ ] The game is playable end-to-end: can navigate, fight monsters, pick up and use items, descend stairs, and die or win
- [ ] Save/load round-trips correctly (save on level 3, close tab, reopen, verify level 3 state)

---

## Quality / Safety Gates

| Gate | Criterion | Evidence |
|------|-----------|----------|
| Build | WASM build succeeds | `cargo build --target wasm32-unknown-unknown` exits 0 |
| Format | Code is formatted | `cargo fmt --check` exits 0 |
| Unit tests | All system fidelity tests pass | `cargo test` exits 0 (18 unit tests) |
| Integration tests | All gameplay scenario tests pass | `cargo test` exits 0 (11 integration tests) |
| Deliverable | HTML file exists and loads | File exists, references WASM, no console errors |
| Artifact hygiene | No build artifacts in git (tracked or untracked) | `git diff` + `git ls-files --others` clean of target/node_modules/dist/pkg |
| Security | No `unsafe` without justification | Code review: each `unsafe` block has a comment |
| Performance | Game runs at interactive speed | Subjective: no visible lag on keystroke in modern browser |

---

## Non-Goals / Deferrals

- **Wizard password authentication**: The C version's `crypt()`-based password is replaced by a simpler browser-friendly gate. Wizard commands themselves are fully in scope.
- **Score file encryption** (`xcrypt.c`): Not applicable to browser context.
- **Multi-user score isolation**: Not applicable to browser localStorage.
- **Signal handling** (`mach_dep.c` signals): Not applicable to WASM.
- **Native binary**: Not a deliverable. Tests run natively but the game targets WASM only.
- **Original save format compatibility**: localStorage format need not match C binary save format.
- **Pixel-perfect terminal rendering**: Faithful ASCII, not pixel-matched to any specific terminal emulator.
