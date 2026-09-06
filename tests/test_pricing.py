"""Pricing and budget tests. Pure logic - no GUI, no database.

    python -m pytest tests/ -v
    python tests/test_pricing.py      # also works without pytest
"""

import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import opencode_monitor as odo  # noqa: E402


def write_prices(models, reference=None, path=None):
    doc = {"models": models}
    if reference:
        doc["reference_model"] = reference
    path = path or tempfile.mktemp(suffix=".json")
    with open(path, "w", encoding="utf-8") as f:
        json.dump(doc, f)
    return path


RATES = {
    "input": 3.0, "output": 15.0, "cache_read": 0.3, "cache_write": 3.75,
}


class TestPriceBook(unittest.TestCase):

    def setUp(self):
        self.p = write_prices(
            {"acme/sonnet": dict(name="Sonnet", **RATES),
             "acme/cheap": {"name": "Cheap", "input": 0.1, "output": 0.4,
                            "cache_read": 0.01, "cache_write": 0.1}},
            reference="acme/sonnet")
        self.pb = odo.PriceBook(self.p)

    def tearDown(self):
        for f in (self.p,):
            if os.path.exists(f):
                os.remove(f)

    def test_input_output_cost(self):
        # 1M input @3 + 100k output @15 = 3.00 + 1.50
        c = self.pb.cost_of("acme/sonnet", 1_000_000, 100_000, 0, 0)
        self.assertAlmostEqual(c, 4.50, places=9)

    def test_cache_is_priced(self):
        """Cache is ~83% of real traffic; ignoring it understates cost ~8x."""
        c = self.pb.cost_of("acme/sonnet", 0, 0, 1_000_000, 1_000_000)
        self.assertAlmostEqual(c, 0.3 + 3.75, places=9)

    def test_unknown_model_is_free_but_flagged(self):
        self.assertAlmostEqual(
            self.pb.cost_of("nope/missing", 1_000_000, 1_000_000, 0, 0), 0.0)
        self.assertTrue(self.pb.entry("nope/missing").get("unknown"))

    def test_free_detection_by_suffix(self):
        self.assertTrue(self.pb.is_free("x/llama-free"))
        self.assertTrue(self.pb.is_free("x/some-sovereign-model"))
        self.assertFalse(self.pb.is_free("acme/sonnet"))

    def test_reference_model_drives_savings(self):
        s = self.pb.shadow_cost("x/llama-free", 1_000_000, 100_000, 0, 0)
        self.assertAlmostEqual(s, 4.50, places=9)

    def test_missing_reference_falls_back_not_zero(self):
        """A bad reference must not silently zero out savings."""
        p = write_prices({"acme/sonnet": dict(name="S", **RATES)},
                         reference="does/not-exist")
        pb = odo.PriceBook(p)
        self.assertIn(pb.reference_model, pb.models)
        self.assertGreater(pb.shadow_cost("x/f-free", 1_000_000, 0, 0, 0), 0)
        os.remove(p)

    def test_overlay_merges_and_overrides(self):
        ov = write_prices(
            {"acme/sonnet": {"name": "Resold", "input": 3.3, "output": 16.5,
                             "cache_read": 0.33, "cache_write": 4.125},
             "hub/private": dict(name="Priv", **RATES)},
            reference="hub/private")
        pb = odo.PriceBook(self.p, overlay=ov)
        self.assertIn("hub/private", pb.models)                    # added
        self.assertEqual(pb.models["acme/sonnet"]["input"], 3.3)   # overridden
        self.assertEqual(pb.reference_model, "hub/private")        # overridden
        os.remove(ov)


class TestBudget(unittest.TestCase):

    def setUp(self):
        self.f = tempfile.mktemp(suffix=".json")
        self.b = odo.Budget(self.f)
        self.b.cfg.update(enabled=True, session_limit_usd=1.0,
                          warn_at_percent=80)

    def tearDown(self):
        if os.path.exists(self.f):
            os.remove(self.f)

    def test_states(self):
        self.assertEqual(self.b.status(0.10)[0], "ok")
        self.assertEqual(self.b.status(0.85)[0], "warn")
        self.assertEqual(self.b.status(1.20)[0], "over")

    def test_status_measures_even_when_disabled(self):
        """status() is a measurement; `enabled` governs enforcement only.

        It used to return ('ok', 0.0) whenever disabled, which made the
        published ledger read as if nothing had been spent.
        """
        self.b.cfg["enabled"] = False
        state, frac = self.b.status(9999.0)
        self.assertEqual(state, "over")
        self.assertGreater(frac, 0)
        # ...but nothing is enforced
        self.assertFalse(self.b.blocks())

    def test_disabled_never_blocks(self):
        self.b.cfg["enabled"] = False
        self.assertFalse(self.b.blocks())

    def test_zero_limit_never_blocks(self):
        self.b.cfg["session_limit_usd"] = 0
        self.assertEqual(self.b.status(50.0)[0], "ok")
        self.assertEqual(self.b.status(50.0)[1], 0.0)

    def test_atomic_save_is_valid_json(self):
        self.b.save({"ses_x": {"cost": 1.0, "state": "over"}})
        with open(self.f, encoding="utf-8") as fh:
            doc = json.load(fh)
        self.assertTrue(doc["enabled"])
        self.assertIn("ses_x", doc["sessions"])
        self.assertIn("updated", doc)


class TestEnforcementModes(unittest.TestCase):
    """warn / soft / hard tiering and the grace allowance."""

    def setUp(self):
        self.f = tempfile.mktemp(suffix=".json")
        self.b = odo.Budget(self.f)
        self.b.cfg.update(enabled=True, session_limit_usd=10.0,
                          warn_at_percent=80, mode="soft",
                          hard_stop_at=1.5, grace_turns=1)

    def tearDown(self):
        if os.path.exists(self.f):
            os.remove(self.f)

    def test_mode_defaults_to_soft(self):
        b = odo.Budget(tempfile.mktemp(suffix=".json"))
        self.assertEqual(b.mode, "soft")

    def test_invalid_mode_falls_back_to_soft(self):
        self.b.cfg["mode"] = "banana"
        self.assertEqual(self.b.mode, "soft")

    def test_warn_mode_never_blocks(self):
        self.b.cfg["mode"] = "warn"
        self.assertFalse(self.b.blocks())
        # still reports the state, it just is not enforced
        self.assertEqual(self.b.status(20.0)[0], "over")

    def test_measurement_is_independent_of_enforcement(self):
        """Every enabled/mode combination reports the same true fraction."""
        for enabled in (True, False):
            for mode in odo.Budget.MODES:
                self.b.cfg.update(enabled=enabled, mode=mode)
                state, frac = self.b.status(15.0)   # 1.5x of a $10 limit
                self.assertEqual(state, "over", f"{enabled}/{mode}")
                self.assertAlmostEqual(frac, 1.5, places=6)

    def test_hard_and_soft_block(self):
        for m in ("soft", "hard"):
            self.b.cfg["mode"] = m
            self.assertTrue(self.b.blocks(), m)

    def test_grace_only_in_soft_mode(self):
        self.b.cfg["mode"] = "soft"
        self.assertEqual(self.b.grace_turns, 1)
        self.b.cfg["mode"] = "hard"
        self.assertEqual(self.b.grace_turns, 0)
        self.b.cfg["mode"] = "warn"
        self.assertEqual(self.b.grace_turns, 0)

    def test_disabled_never_blocks_regardless_of_mode(self):
        self.b.cfg.update(enabled=False, mode="hard")
        self.assertFalse(self.b.blocks())

    def test_block_when_exceeded_false_disables_blocking(self):
        self.b.cfg["block_when_exceeded"] = False
        self.assertFalse(self.b.blocks())

    def test_hard_stop_multiplier_sane(self):
        self.b.cfg["hard_stop_at"] = 0.5      # below 1x makes no sense
        self.assertGreaterEqual(self.b.hard_stop_at, 1.0)
        self.b.cfg["hard_stop_at"] = "junk"
        self.assertEqual(self.b.hard_stop_at, 1.5)


class TestGracePublishing(unittest.TestCase):
    """grace_remaining must reflect mode, usage and the hard ceiling."""

    def setUp(self):
        self.f = tempfile.mktemp(suffix=".json")
        b = odo.Budget(self.f)
        b.cfg.update(enabled=True, session_limit_usd=10.0, mode="soft",
                     hard_stop_at=1.5, grace_turns=1)
        self.b = b

    def tearDown(self):
        if os.path.exists(self.f):
            os.remove(self.f)

    def remaining(self, eff_cost, used=0, mode="soft"):
        """Mirror of the grace calculation in publish_budget()."""
        self.b.cfg["mode"] = mode
        state, frac = self.b.status(eff_cost)
        if state != "over" or self.b.mode != "soft":
            return 0
        if frac >= self.b.hard_stop_at:
            return 0
        return max(0, self.b.grace_turns - used)

    def test_grace_offered_on_first_breach(self):
        self.assertEqual(self.remaining(11.0, used=0), 1)

    def test_grace_exhausted_after_use(self):
        self.assertEqual(self.remaining(11.0, used=1), 0)

    def test_no_grace_past_hard_ceiling(self):
        # 16.0 / 10.0 = 1.6x, above the 1.5x hard stop
        self.assertEqual(self.remaining(16.0, used=0), 0)

    def test_no_grace_in_hard_mode(self):
        self.assertEqual(self.remaining(11.0, used=0, mode="hard"), 0)

    def test_no_grace_when_under_limit(self):
        self.assertEqual(self.remaining(5.0, used=0), 0)


class TestBudgetBaseline(unittest.TestCase):
    """Enabling a limit must not retroactively block already-spent sessions."""

    def setUp(self):
        self.f = tempfile.mktemp(suffix=".json")
        b = odo.Budget(self.f)
        b.cfg.update(enabled=True, session_limit_usd=1.0, warn_at_percent=80)

        class Host:
            effective_session_cost = odo.OpenCodeOdometer.effective_session_cost
        self.h = Host()
        self.h.budget = b
        self.h.state_data = {"budget_baselines": {"ses_old": 17.27}}

    def tearDown(self):
        if os.path.exists(self.f):
            os.remove(self.f)

    def state(self, sid, raw):
        eff = self.h.effective_session_cost(sid, raw)
        return self.h.budget.status(eff)[0], eff

    def test_enabling_does_not_block_existing_spend(self):
        st, eff = self.state("ses_old", 17.27)
        self.assertEqual(eff, 0.0)
        self.assertEqual(st, "ok")

    def test_counts_only_spend_after_enable(self):
        self.assertEqual(self.state("ses_old", 17.77)[0], "ok")    # +0.50
        self.assertEqual(self.state("ses_old", 18.12)[0], "warn")  # +0.85
        self.assertEqual(self.state("ses_old", 18.40)[0], "over")  # +1.13

    def test_new_session_counts_from_zero(self):
        self.assertEqual(self.state("ses_new", 0.10)[0], "ok")
        self.assertEqual(self.state("ses_new", 1.50)[0], "over")

    def test_baseline_never_goes_negative(self):
        # a re-priced session can dip below its baseline; must clamp at 0
        self.assertEqual(self.h.effective_session_cost("ses_old", 5.0), 0.0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
