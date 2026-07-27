import { parseDataFileExtensions, isDataFilePath, utf8ByteLength } from '../src/datafiles';

describe('parseDataFileExtensions', () => {
  test('parses the default setting', () => {
    expect(parseDataFileExtensions('base, csv, json, yaml, yml'))
      .toEqual(['base', 'csv', 'json', 'yaml', 'yml']);
  });

  test('tolerates dots, case, blanks and duplicates', () => {
    expect(parseDataFileExtensions(' .CSV , csv,, JSON ')).toEqual(['csv', 'json']);
  });

  test('drops md — notes have their own sync path', () => {
    expect(parseDataFileExtensions('csv, md, MD')).toEqual(['csv']);
  });

  test('empty input yields no extensions', () => {
    expect(parseDataFileExtensions('')).toEqual([]);
    expect(parseDataFileExtensions('  ,  ,')).toEqual([]);
  });

  test('drops malformed entries instead of sending them to the backend', () => {
    expect(parseDataFileExtensions('csv;json')).toEqual([]);
    expect(parseDataFileExtensions('cs v')).toEqual([]);
    expect(parseDataFileExtensions('c*v')).toEqual([]);
    expect(parseDataFileExtensions('toolongextension')).toEqual([]);
  });

  test('keeps well-formed entries alongside a dropped malformed one', () => {
    expect(parseDataFileExtensions('csv, c*v, json')).toEqual(['csv', 'json']);
  });
});

describe('isDataFilePath', () => {
  const exts = ['csv', 'base'];

  test('matches on extension, case-insensitively', () => {
    expect(isDataFilePath('data/a.csv', exts)).toBe(true);
    expect(isDataFilePath('data/A.CSV', exts)).toBe(true);
    expect(isDataFilePath('views/tasks.base', exts)).toBe(true);
  });

  test('rejects non-matching and extensionless paths', () => {
    expect(isDataFilePath('notes/a.md', exts)).toBe(false);
    expect(isDataFilePath('LICENSE', exts)).toBe(false);
    expect(isDataFilePath('a.csv.bak', exts)).toBe(false);
  });

  test('a dot in a directory name is not an extension', () => {
    expect(isDataFilePath('my.data/file', exts)).toBe(false);
  });

  test('dotfiles are not treated as having an extension', () => {
    expect(isDataFilePath('.gitignore', exts)).toBe(false);
    expect(isDataFilePath('dir/.env', exts)).toBe(false);
  });
});

describe('utf8ByteLength', () => {
  test('counts bytes, not UTF-16 code units', () => {
    expect(utf8ByteLength('abc')).toBe(3);
    expect(utf8ByteLength('данные')).toBe(12);
  });
});
