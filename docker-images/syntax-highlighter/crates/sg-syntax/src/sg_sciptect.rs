#![allow(dead_code)]
use std::{
    collections::{HashMap, HashSet},
    fmt::Debug,
};

use protobuf::{EnumOrUnknown, SpecialFields};
use scip::types::{Document, Occurrence, SyntaxKind};
use syntect::{
    parsing::{BasicScopeStackOp, ParseState, ScopeStack, SyntaxReference, SyntaxSet, SCOPE_REPO},
    util::LinesWithEndings,
};

/// The RangeGenerator generate a Document with occurrences set to the corresponding syntax kinds
///
/// If max_line_len is not None, any lines with length greater than the
/// provided number will not be highlighted.
pub struct DocumentGenerator<'a> {
    syntax_set: &'a SyntaxSet,
    parse_state: ParseState,
    stack: ScopeStack,
    code: &'a str,
    max_line_len: Option<usize>,
}

#[derive(Clone)]
struct PartialHighlight {
    start_row: i32,
    start_col: i32,
    kind: Option<SyntaxKind>,
}

impl PartialHighlight {
    fn some(start_row: usize, start_col: usize, kind: SyntaxKind) -> Self {
        Self {
            start_row: start_row as i32,
            start_col: start_col as i32,
            kind: Some(kind),
        }
    }

    fn none() -> Self {
        Self {
            start_row: 0,
            start_col: 0,
            kind: None,
        }
    }
}

impl Debug for PartialHighlight {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self.kind {
            Some(kind) => write!(
                f,
                "PartialHighight({}, {}, {:?})",
                self.start_row, self.start_col, kind
            ),
            None => write!(f, "<None>",),
        }
    }
}

#[derive(Default)]
struct HighlightManager {
    highlights: Vec<PartialHighlight>,
}

// HighlightManager is used to keep track of the scope of highlights that we have and make sure
// that we never push overlapping ranges and that we always have ranges sorted by start offset
// (that part we should get for free).
//
// So given a situation like this:
// "asdf"
// ^        Punctuation
// ^^^^^^   String
//      ^   Punctuation
//
// HighlightManager will transform this to:
//
// "asdf"
// ^        Punctuation
//  ^^^^    String
//      ^   Punctuation
//
// Note: The parts where string previous overlapped the punctuation
// is no longer the case.
impl HighlightManager {
    fn push_hl(&mut self, hl: PartialHighlight) -> Option<PartialHighlight> {
        let mut existing_hl = None;
        if let Some(last_hl) = self.highlights.last_mut() {
            if let Some(_kind) = last_hl.kind {
                existing_hl = Some(last_hl.clone());
                last_hl.start_row = hl.start_row;
                last_hl.start_col = hl.start_col;
            }
        }

        self.highlights.push(hl);

        existing_hl
    }

    fn pop_hl(&mut self, row: usize, character: usize) -> Option<PartialHighlight> {
        let row = row as i32;
        let character = character as i32;

        let hl = self.highlights.pop();
        if let Some(hl) = &hl {
            // Modify all previous highlights that started at this location.
            //  Make sure that we set their start row and column to whatever this partial
            //  highlight is ending at. This makes sure that we don't have any overlapping
            //  highlights.
            for prev_hl in self.highlights.iter_mut().rev() {
                if prev_hl.start_row != hl.start_row || prev_hl.start_col != hl.start_col {
                    break;
                }

                prev_hl.start_row = row;
                prev_hl.start_col = character;
            }
        }

        hl
    }
}

impl Debug for HighlightManager {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        if self.highlights.is_empty() {
            return write!(f, "HighlightManager(None)");
        }

        write!(f, "HighlightManager {{\n")?;
        for hl in self.highlights.iter() {
            write!(f, "  {:?}\n", hl)?;
        }

        write!(f, "}}")
    }
}

impl<'a> DocumentGenerator<'a> {
    pub fn new(
        ss: &'a SyntaxSet,
        sr: &SyntaxReference,
        code: &'a str,
        max_line_len: Option<usize>,
    ) -> Self {
        Self {
            code,
            syntax_set: ss,
            parse_state: ParseState::new(sr),
            stack: ScopeStack::new(),
            max_line_len,
        }
    }

    // generate takes ownership of self so that it can't be re-used
    pub fn generate(mut self) -> Document {
        // TODO: We need to handle multiple scope lengths here
        //          Possibly the entire scope list but it's SO annoyingly large...
        //          Not sure how you would even do all of them.
        let mut scope_mapping = HashMap::new();
        scope_mapping.insert("keyword".to_string(), SyntaxKind::IdentifierKeyword);
        scope_mapping.insert("variable".to_string(), SyntaxKind::Identifier);
        scope_mapping.insert("punctuation".to_string(), SyntaxKind::PunctuationBracket);
        scope_mapping.insert("string".to_string(), SyntaxKind::StringLiteral);

        let mut ignore_mapping = HashSet::new();
        ignore_mapping.insert("source");

        let mut unhandled_scopes = HashSet::new();

        let mut document = Document::default();

        let mut highlight_manager = HighlightManager::default();
        for (row, line_contents) in LinesWithEndings::from(self.code).enumerate() {
            if self.max_line_len.map_or(false, |n| line_contents.len() > n) {
                // TODO: Should just gracefully handle this, but haven't been able
                // to reproduce this yet.
                panic!("Made it past end of line? {:?} {:?}", row, line_contents);
                // self.write_escaped_html(line);
            }

            let ops = self.parse_state.parse_line(line_contents, self.syntax_set);
            for &(byte_offset, ref op) in ops.as_slice() {
                // Character represents the nth character in a line.
                // This can be roughly thought of as column, but non-single-width
                // characters confuse this situation.
                let character = match line_contents
                    .char_indices()
                    .enumerate()
                    .find(|(_, (offset, _))| *offset == byte_offset)
                {
                    Some(char) => char,
                    None => continue,
                }
                .0;

                // It's unclear to me why we have to clone the entire stack here?
                //  It should work without cloning (as far as I can tell)
                //  because we just set the value back afterwards
                //
                // TODO
                // let mut stack = self.stack.clone();
                self.stack
                    .apply_with_hook(op, |basic_op, _| match basic_op {
                        BasicScopeStackOp::Push(scope) => {
                            let atom = scope.atom_at(0 as usize);

                            // Release lock as quickly as we can
                            let atom_s = {
                                let repo = SCOPE_REPO.lock().unwrap();
                                repo.atom_str(atom).to_string()
                            };

                            if ignore_mapping.contains(atom_s.as_str()) {
                                highlight_manager.push_hl(PartialHighlight::none());
                                return;
                            }

                            match scope_mapping.get(atom_s.as_str()) {
                                Some(kind) => {
                                    let partial_hl = PartialHighlight::some(row, character, *kind);
                                    if let Some(partial_hl) = highlight_manager.push_hl(partial_hl)
                                    {
                                        push_document_occurence(
                                            &mut document,
                                            partial_hl,
                                            row,
                                            character,
                                        );
                                    };
                                }
                                None => {
                                    unhandled_scopes.insert(scope);
                                    return;
                                }
                            }
                        }
                        BasicScopeStackOp::Pop => {
                            if let Some(partial_hl) = highlight_manager.pop_hl(row, character) {
                                push_document_occurence(&mut document, partial_hl, row, character);
                            }
                        }
                    });
                // self.stack = stack;
            }

            let end_of_line = (row, line_contents.chars().count());
            while let Some(partial_hl) = highlight_manager.pop_hl(end_of_line.0, end_of_line.1) {
                push_document_occurence(&mut document, partial_hl, end_of_line.0, end_of_line.1);
            }
        }

        // TODO: I think (from my logic) this might not be necessary :)
        // document.occurrences.sort_by_key(|o| o.range.clone());

        if highlight_manager.highlights.len() > 0 {
            panic!("unhandled highlights in: {:?}", highlight_manager);
        }

        if !unhandled_scopes.is_empty() {
            // This is where we can add a bunch of these before merging
            // panic!("Unhandled Scopes: {:?}", unhandled_scopes);
        }

        document
    }
}

fn push_document_occurence(
    document: &mut Document,
    partial_hl: PartialHighlight,
    row: usize,
    col: usize,
) {
    let row = row as i32;
    let col = col as i32;

    // Do not emit ranges that are empty
    if (partial_hl.start_row, partial_hl.start_col) == (row, col) {
        return;
    }

    if let Some(kind) = partial_hl.kind {
        document.occurrences.push(new_occurence(
            vec![partial_hl.start_row, partial_hl.start_col, row, col],
            kind,
        ))
    }
}

fn new_occurence(range: Vec<i32>, syntax_kind: SyntaxKind) -> Occurrence {
    let syntax_kind = EnumOrUnknown::new(syntax_kind);

    Occurrence {
        range,
        syntax_kind,
        symbol_roles: 0,
        symbol: String::default(),
        override_documentation: vec![],
        diagnostics: vec![],
        special_fields: SpecialFields::default(),
    }
}

#[cfg(test)]
mod test {
    use std::{
        fs::{read_dir, File},
        io::Read,
    };

    use pretty_assertions::assert_eq;
    use unicode_width::UnicodeWidthStr;

    use super::*;
    use crate::{determine_language, dump_document};

    #[test]
    fn test_generates_empty_file() {
        let syntax_set = SyntaxSet::load_defaults_newlines();
        let mut q = crate::SourcegraphQuery::default();
        q.filetype = Some("go".to_string());
        q.code = "".to_string();

        let syntax_def = determine_language(&q, &syntax_set).unwrap();
        let output = DocumentGenerator::new(&syntax_set, syntax_def, &q.code, q.line_length_limit)
            .generate();

        assert_eq!(Document::default(), output);
    }

    #[test]
    fn test_generates_go_package() {
        let syntax_set = SyntaxSet::load_defaults_newlines();
        let mut q = crate::SourcegraphQuery::default();
        q.filetype = Some("go".to_string());
        q.code = "package main".to_string();

        let syntax_def = determine_language(&q, &syntax_set).unwrap();
        let output = DocumentGenerator::new(&syntax_set, syntax_def, &q.code, q.line_length_limit)
            .generate();

        assert_eq!(
            vec![
                new_occurence(vec![0, 0, 0, 7], SyntaxKind::IdentifierKeyword),
                new_occurence(vec![0, 8, 0, 12], SyntaxKind::Identifier),
            ],
            output.occurrences
        );
    }

    #[test]
    fn test_generates_cs_multibyte() {
        let syntax_set = SyntaxSet::load_defaults_newlines();
        let mut q = crate::SourcegraphQuery::default();
        // q.filetype = Some("csharp".to_string());
        q.filepath = "multibyte.cs".to_string();
        q.code = r#"
"inner string";
"#
        .to_string();

        let syntax_def = determine_language(&q, &syntax_set).unwrap();
        let output = DocumentGenerator::new(&syntax_set, syntax_def, &q.code, q.line_length_limit)
            .generate();

        assert_eq!(
            vec![
                new_occurence(vec![1, 0, 1, 1], SyntaxKind::PunctuationBracket),
                new_occurence(vec![1, 1, 1, 13], SyntaxKind::StringLiteral),
                new_occurence(vec![1, 13, 1, 14], SyntaxKind::PunctuationBracket),
                new_occurence(vec![1, 14, 1, 15], SyntaxKind::PunctuationBracket),
            ],
            output.occurrences
        );
    }

    #[test]
    fn test_all_files() -> Result<(), std::io::Error> {
        let dir = read_dir("./src/snapshots/syntect_files/")?;
        for entry in dir {
            let entry = entry?;
            let filepath = entry.path();
            let mut file = File::open(&filepath)?;
            let mut contents = String::new();
            file.read_to_string(&mut contents)?;

            // let filetype = &determine_filetype(&SourcegraphQuery {
            //     extension: filepath.extension().unwrap().to_str().unwrap().to_string(),
            //     filepath: filepath.to_str().unwrap().to_string(),
            //     filetype: None,
            //     css: false,
            //     line_length_limit: None,
            //     theme: "".to_string(),
            //     code: contents.clone(),
            // });

            let mut q = crate::SourcegraphQuery::default();
            q.filetype = Some("go".to_string());
            q.code = contents.clone();
            let syntax_set = SyntaxSet::load_defaults_newlines();
            let syntax_def = determine_language(&q, &syntax_set).unwrap();

            let document =
                DocumentGenerator::new(&syntax_set, syntax_def, &q.code, q.line_length_limit)
                    .generate();

            insta::assert_snapshot!(
                filepath
                    .to_str()
                    .unwrap()
                    .replace("/src/snapshots/syntect_files", ""),
                dump_document(&document, &contents)
            );
        }

        Ok(())
    }

    #[test]
    fn test_various_unicode_characters() {
        // 1 double width char
        assert_eq!(2, UnicodeWidthStr::width("世"));

        // 3 single width chars, 10 double width chars -> 23
        assert_eq!(23, UnicodeWidthStr::width("Ｈｅｌｌｏ, ｗｏｒｌｄ!"));

        // 1 emoji is double width
        assert_eq!(2, UnicodeWidthStr::width("🥳"));
        assert_eq!(2, UnicodeWidthStr::width("👩"),); // Woman
        assert_eq!(2, UnicodeWidthStr::width("🔬")); // Microscope

        // This one is confusing because it's two emojis with a zero-width
        // item combining them... I'm not sure how we should handle this case, but for now we will
        // leave it like this.
        //
        // So for now it essentially 2 double width emojis + 1 zero-width = 4
        assert_eq!(4, UnicodeWidthStr::width("👩‍🔬")); // Woman scientist
    }
}
