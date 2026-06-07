const fs = require('fs');
let html = fs.readFileSync('admin-dashboard.html', 'utf8');

const targetStr = `      </div>
    </div>

    <!-- REVIEWS -->`;

const replacement = `      </div>
    </div>

  <!-- Modal: Bulk JSON Edit Vacancies -->
  <div class="modal" id="modal-bulk-json">
    <div class="modal-content" style="max-width: 800px; width: 90%;">
      <span class="modal-close" onclick="closeModal('modal-bulk-json')">&times;</span>
      <h2 style="margin-bottom: 20px;">Bulk JSON Edit Vacancies</h2>
      <p style="font-size: 14px; color: var(--text-secondary); margin-bottom: 15px;">
        Here is the JSON representation of all vacancies. You can edit this JSON array directly to mass-update existing vacancies. Click "Save Bulk Updates" to push the changes via API. Do not modify the IDs.
      </p>
      <textarea id="bulk-json-textarea" style="width: 100%; height: 500px; font-family: monospace; padding: 10px; background: #1e1e1e; color: #d4d4d4; border: 1px solid var(--border-color); border-radius: 6px;"></textarea>
      <div style="margin-top: 15px; display: flex; justify-content: flex-end; gap: 10px;">
        <button class="btn btn-ghost" onclick="closeModal('modal-bulk-json')">Cancel</button>
        <button class="btn btn-primary" onclick="saveBulkJson()">Save Bulk Updates</button>
      </div>
    </div>
  </div>

    <!-- REVIEWS -->`;

html = html.replace(targetStr, replacement);
fs.writeFileSync('admin-dashboard.html', html);
