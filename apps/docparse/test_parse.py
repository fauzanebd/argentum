"""Unit tests for the classification and rendering halves of parse.py (T-P2).

`unittest` rather than pytest: this service has three runtime dependencies and
adding a test framework to the image's requirements to assert five pure
functions would be the largest thing in it. Run with

    .venv/bin/python -m unittest discover apps/docparse

The parts that need a real PDF are not here — they are the gate's job
(live-gate-backlog.md 1h), because a fixture PDF committed to this repository
would be a fixture somebody trusts more than a tenant's actual file.
"""

from __future__ import annotations

import unittest

from parse import alnum_ratio, classify, to_markdown


class TestAlnumRatio(unittest.TestCase):
    def test_prose_scores_high(self):
        self.assertGreater(alnum_ratio("Laporan Penjualan Q4 2024, total Rp 3.863.405.700"), 0.95)

    def test_indonesian_prose_scores_high(self):
        self.assertGreater(alnum_ratio("Pendapatan kuartal keempat naik 12% dibanding tahun lalu."), 0.95)

    # The failure this ratio exists for: a subsetted font with no ToUnicode map
    # decodes to private-use code points, so the page "has text" and the text is
    # garbage. Nothing else in the pipeline can tell.
    def test_broken_font_map_collapses(self):
        self.assertLess(alnum_ratio(""), 0.6)

    def test_empty_is_zero_not_one(self):
        self.assertEqual(alnum_ratio(""), 0.0)


class TestClassify(unittest.TestCase):
    def test_ordinary_page_is_text(self):
        self.assertEqual(classify(char_count=812, ratio=0.98, images=0.0), "text")

    def test_scan_with_a_stamp_is_needs_ocr(self):
        # A handful of characters from a header is not a text layer.
        self.assertEqual(classify(char_count=12, ratio=1.0, images=0.95), "needs_ocr")

    def test_garbled_font_is_needs_ocr(self):
        self.assertEqual(classify(char_count=900, ratio=0.2, images=0.0), "needs_ocr")

    # A report page that is mostly chart is still a report page. Classifying it
    # as a scan would send a readable page to the model in T-P3 and pay for
    # text we already have.
    def test_chart_heavy_page_with_real_text_stays_text(self):
        self.assertEqual(classify(char_count=640, ratio=0.97, images=0.9), "text")

    def test_empty_page_is_needs_ocr_not_text(self):
        self.assertEqual(classify(char_count=0, ratio=0.0, images=0.0), "needs_ocr")


class TestToMarkdown(unittest.TestCase):
    def test_table_becomes_a_pipe_table(self):
        md = to_markdown(
            "Laporan Penjualan",
            [{"rows": [["Region", "Revenue"], ["Jakarta", "3.863.405.700"]], "col_count": 2}],
        )
        self.assertIn("| Region | Revenue |", md)
        self.assertIn("| Jakarta | 3.863.405.700 |", md)
        self.assertIn("| --- | --- |", md)

    # A pipe inside a cell would split it into two columns and a newline would
    # end the row — both silently, and both producing a table that is wrong
    # rather than a table that fails.
    def test_pipes_and_newlines_in_cells_are_escaped(self):
        md = to_markdown("", [{"rows": [["a|b", "c\nd"], ["1", "2"]], "col_count": 2}])
        self.assertIn(r"a\|b", md)
        self.assertNotIn("c\nd", md)

    def test_short_rows_are_padded_to_the_width(self):
        md = to_markdown("", [{"rows": [["A", "B", "C"], ["1"]], "col_count": 3}])
        self.assertIn("| 1 |  |  |", md)

    def test_no_tables_is_just_the_text(self):
        self.assertEqual(to_markdown("Halaman satu", []), "Halaman satu")


if __name__ == "__main__":
    unittest.main()
