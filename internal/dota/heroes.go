// Package dota holds static Dota 2 reference data.
package dota

const (
	portraitBase = "https://cdn.cloudflare.steamstatic.com/apps/dota2/images/dota_react/heroes/"
	iconBase     = "https://cdn.cloudflare.steamstatic.com/apps/dota2/images/dota_react/heroes/icons/"
)

// hero is a hero's display name plus its asset slug, which is the
// npc_dota_hero_ name with the prefix stripped.
type hero struct {
	name string
	slug string
}

// heroes is generated from OpenDota's hero constants. Valve adds heroes every
// few months, so an unknown id is an expected condition rather than an error;
// every lookup below degrades to an empty string so callers and templates can
// fall back instead of breaking.
var heroes = map[int32]hero{
	1:   {name: "Anti-Mage", slug: "antimage"},
	2:   {name: "Axe", slug: "axe"},
	3:   {name: "Bane", slug: "bane"},
	4:   {name: "Bloodseeker", slug: "bloodseeker"},
	5:   {name: "Crystal Maiden", slug: "crystal_maiden"},
	6:   {name: "Drow Ranger", slug: "drow_ranger"},
	7:   {name: "Earthshaker", slug: "earthshaker"},
	8:   {name: "Juggernaut", slug: "juggernaut"},
	9:   {name: "Mirana", slug: "mirana"},
	10:  {name: "Morphling", slug: "morphling"},
	11:  {name: "Shadow Fiend", slug: "nevermore"},
	12:  {name: "Phantom Lancer", slug: "phantom_lancer"},
	13:  {name: "Puck", slug: "puck"},
	14:  {name: "Pudge", slug: "pudge"},
	15:  {name: "Razor", slug: "razor"},
	16:  {name: "Sand King", slug: "sand_king"},
	17:  {name: "Storm Spirit", slug: "storm_spirit"},
	18:  {name: "Sven", slug: "sven"},
	19:  {name: "Tiny", slug: "tiny"},
	20:  {name: "Vengeful Spirit", slug: "vengefulspirit"},
	21:  {name: "Windranger", slug: "windrunner"},
	22:  {name: "Zeus", slug: "zuus"},
	23:  {name: "Kunkka", slug: "kunkka"},
	25:  {name: "Lina", slug: "lina"},
	26:  {name: "Lion", slug: "lion"},
	27:  {name: "Shadow Shaman", slug: "shadow_shaman"},
	28:  {name: "Slardar", slug: "slardar"},
	29:  {name: "Tidehunter", slug: "tidehunter"},
	30:  {name: "Witch Doctor", slug: "witch_doctor"},
	31:  {name: "Lich", slug: "lich"},
	32:  {name: "Riki", slug: "riki"},
	33:  {name: "Enigma", slug: "enigma"},
	34:  {name: "Tinker", slug: "tinker"},
	35:  {name: "Sniper", slug: "sniper"},
	36:  {name: "Necrophos", slug: "necrolyte"},
	37:  {name: "Warlock", slug: "warlock"},
	38:  {name: "Beastmaster", slug: "beastmaster"},
	39:  {name: "Queen of Pain", slug: "queenofpain"},
	40:  {name: "Venomancer", slug: "venomancer"},
	41:  {name: "Faceless Void", slug: "faceless_void"},
	42:  {name: "Wraith King", slug: "skeleton_king"},
	43:  {name: "Death Prophet", slug: "death_prophet"},
	44:  {name: "Phantom Assassin", slug: "phantom_assassin"},
	45:  {name: "Pugna", slug: "pugna"},
	46:  {name: "Templar Assassin", slug: "templar_assassin"},
	47:  {name: "Viper", slug: "viper"},
	48:  {name: "Luna", slug: "luna"},
	49:  {name: "Dragon Knight", slug: "dragon_knight"},
	50:  {name: "Dazzle", slug: "dazzle"},
	51:  {name: "Clockwerk", slug: "rattletrap"},
	52:  {name: "Leshrac", slug: "leshrac"},
	53:  {name: "Nature's Prophet", slug: "furion"},
	54:  {name: "Lifestealer", slug: "life_stealer"},
	55:  {name: "Dark Seer", slug: "dark_seer"},
	56:  {name: "Clinkz", slug: "clinkz"},
	57:  {name: "Omniknight", slug: "omniknight"},
	58:  {name: "Enchantress", slug: "enchantress"},
	59:  {name: "Huskar", slug: "huskar"},
	60:  {name: "Night Stalker", slug: "night_stalker"},
	61:  {name: "Broodmother", slug: "broodmother"},
	62:  {name: "Bounty Hunter", slug: "bounty_hunter"},
	63:  {name: "Weaver", slug: "weaver"},
	64:  {name: "Jakiro", slug: "jakiro"},
	65:  {name: "Batrider", slug: "batrider"},
	66:  {name: "Chen", slug: "chen"},
	67:  {name: "Spectre", slug: "spectre"},
	68:  {name: "Ancient Apparition", slug: "ancient_apparition"},
	69:  {name: "Doom", slug: "doom_bringer"},
	70:  {name: "Ursa", slug: "ursa"},
	71:  {name: "Spirit Breaker", slug: "spirit_breaker"},
	72:  {name: "Gyrocopter", slug: "gyrocopter"},
	73:  {name: "Alchemist", slug: "alchemist"},
	74:  {name: "Invoker", slug: "invoker"},
	75:  {name: "Silencer", slug: "silencer"},
	76:  {name: "Outworld Devourer", slug: "obsidian_destroyer"},
	77:  {name: "Lycan", slug: "lycan"},
	78:  {name: "Brewmaster", slug: "brewmaster"},
	79:  {name: "Shadow Demon", slug: "shadow_demon"},
	80:  {name: "Lone Druid", slug: "lone_druid"},
	81:  {name: "Chaos Knight", slug: "chaos_knight"},
	82:  {name: "Meepo", slug: "meepo"},
	83:  {name: "Treant Protector", slug: "treant"},
	84:  {name: "Ogre Magi", slug: "ogre_magi"},
	85:  {name: "Undying", slug: "undying"},
	86:  {name: "Rubick", slug: "rubick"},
	87:  {name: "Disruptor", slug: "disruptor"},
	88:  {name: "Nyx Assassin", slug: "nyx_assassin"},
	89:  {name: "Naga Siren", slug: "naga_siren"},
	90:  {name: "Keeper of the Light", slug: "keeper_of_the_light"},
	91:  {name: "Io", slug: "wisp"},
	92:  {name: "Visage", slug: "visage"},
	93:  {name: "Slark", slug: "slark"},
	94:  {name: "Medusa", slug: "medusa"},
	95:  {name: "Troll Warlord", slug: "troll_warlord"},
	96:  {name: "Centaur Warrunner", slug: "centaur"},
	97:  {name: "Magnus", slug: "magnataur"},
	98:  {name: "Timbersaw", slug: "shredder"},
	99:  {name: "Bristleback", slug: "bristleback"},
	100: {name: "Tusk", slug: "tusk"},
	101: {name: "Skywrath Mage", slug: "skywrath_mage"},
	102: {name: "Abaddon", slug: "abaddon"},
	103: {name: "Elder Titan", slug: "elder_titan"},
	104: {name: "Legion Commander", slug: "legion_commander"},
	105: {name: "Techies", slug: "techies"},
	106: {name: "Ember Spirit", slug: "ember_spirit"},
	107: {name: "Earth Spirit", slug: "earth_spirit"},
	108: {name: "Underlord", slug: "abyssal_underlord"},
	109: {name: "Terrorblade", slug: "terrorblade"},
	110: {name: "Phoenix", slug: "phoenix"},
	111: {name: "Oracle", slug: "oracle"},
	112: {name: "Winter Wyvern", slug: "winter_wyvern"},
	113: {name: "Arc Warden", slug: "arc_warden"},
	114: {name: "Monkey King", slug: "monkey_king"},
	119: {name: "Dark Willow", slug: "dark_willow"},
	120: {name: "Pangolier", slug: "pangolier"},
	121: {name: "Grimstroke", slug: "grimstroke"},
	123: {name: "Hoodwink", slug: "hoodwink"},
	126: {name: "Void Spirit", slug: "void_spirit"},
	128: {name: "Snapfire", slug: "snapfire"},
	129: {name: "Mars", slug: "mars"},
	131: {name: "Ring Master", slug: "ringmaster"},
	135: {name: "Dawnbreaker", slug: "dawnbreaker"},
	136: {name: "Marci", slug: "marci"},
	137: {name: "Primal Beast", slug: "primal_beast"},
	138: {name: "Muerta", slug: "muerta"},
	145: {name: "Kez", slug: "kez"},
	155: {name: "Largo", slug: "largo"},
}

// Name returns the hero's display name, or "" if the id is unknown.
// Valve uses hero id 0 to mean "not picked yet".
func Name(id int32) string {
	return heroes[id].name
}

// PortraitURL returns the hero's landscape portrait on Valve's CDN, or "" if
// the id is unknown.
func PortraitURL(id int32) string {
	h, ok := heroes[id]
	if !ok {
		return ""
	}
	return portraitBase + h.slug + ".png"
}

// IconURL returns the hero's small square icon on Valve's CDN, or "" if the id
// is unknown.
func IconURL(id int32) string {
	h, ok := heroes[id]
	if !ok {
		return ""
	}
	return iconBase + h.slug + ".png"
}
