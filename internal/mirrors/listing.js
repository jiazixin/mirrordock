(() => {
  const body = document.getElementById('file-rows');
  const rows = Array.from(body.rows);
  const search = document.querySelector('input[type="search"]');
  const sorts = Array.from(document.querySelectorAll('[data-sort]'));
  const empty = document.getElementById('empty-state');
  const count = document.getElementById('result-count');
  const reset = document.getElementById('reset-filters');
  const collator = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' });
  let sort = 'name';
  let descending = false;

  function render() {
    const query = search.value.trim().toLocaleLowerCase();
    const matching = rows.filter(row => row.dataset.name.toLocaleLowerCase().includes(query));
    matching.sort((a, b) => {
      if (a.dataset.dir !== b.dataset.dir) return a.dataset.dir === 'true' ? -1 : 1;
      let order;
      if (sort === 'bytes') order = Number(a.dataset.bytes) - Number(b.dataset.bytes);
      else if (['modified', 'last', 'next'].includes(sort)) order = (Date.parse(a.dataset[sort]) || 0) - (Date.parse(b.dataset[sort]) || 0);
      else order = collator.compare(a.dataset[sort], b.dataset[sort]);
      return (descending ? -order : order) || collator.compare(a.dataset.name, b.dataset.name);
    });
    for (const row of rows) row.hidden = true;
    for (const row of matching) body.append(row);
    for (const row of matching) row.hidden = false;
    empty.hidden = matching.length !== 0;
    empty.querySelector('h3').textContent = rows.length ? '没有匹配的文件' : '目录为空';
    empty.querySelector('p').textContent = rows.length ? '试试其他关键词。' : '文件发布后会出现在这里。';
    reset.hidden = !rows.length;
    count.textContent = matching.length ? `${matching.length} 个条目` : '0 个条目';
    if (matching.length !== rows.length) count.textContent += ` · 共 ${rows.length} 项`;
  }

  for (const button of sorts) button.addEventListener('click', () => {
    descending = sort === button.dataset.sort ? !descending : false;
    sort = button.dataset.sort;
    for (const control of sorts) {
      const active = control === button;
      control.closest('th').removeAttribute('aria-sort');
      if (active) control.closest('th').setAttribute('aria-sort', descending ? 'descending' : 'ascending');
      control.querySelector('.sort-arrow').textContent = active ? (descending ? '↓' : '↑') : '↕';
    }
    render();
  });
  search.addEventListener('input', render);
  reset.addEventListener('click', () => {
    search.value = '';
    render();
    search.focus();
  });
  render();
  document.querySelector('.search').hidden = false;
})();
